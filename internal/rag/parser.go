package rag

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// ParseFile extracts plain text from a file based on its extension.
// Supported: .txt .md .csv .json (raw read), .pdf (pdftotext), .docx (zip+xml), .xlsx (excelize), .pptx (zip+xml).
const maxDocumentBytes = 16 << 20
const maxExtractedBytes = 8 << 20

var parseSlot = make(chan struct{}, 1)

func ParseFile(path string) (text string, err error) {
	select {
	case parseSlot <- struct{}{}:
		defer func() { <-parseSlot }()
	default:
		return "", errors.New("another document is being parsed; retry shortly")
	}
	defer func() {
		if recover() != nil {
			text = ""
			err = errors.New("invalid document")
		}
	}()
	snapshot, cleanup, err := documentSnapshot(path)
	if err != nil {
		return "", err
	}
	defer cleanup()
	path = snapshot
	if err = checkDocument(path); err != nil {
		return "", err
	}
	text, err = parseByExt(path)
	if err != nil {
		return "", err
	}
	// Every format goes through the same repair: DOCX and TXT rarely hyphenate,
	// but they do carry curly quotes, non-breaking spaces and soft hyphens, which
	// a query typed on a keyboard will never match. The pass is idempotent.
	if len(text) > maxExtractedBytes {
		return "", errors.New("extracted text exceeds 8 MiB; split the document")
	}
	return NormalizeExtractedText(text), nil
}

func parseByExt(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		return parsePDF(path)
	case ".docx":
		return parseDOCX(path)
	case ".xlsx":
		return parseXLSX(path)
	case ".pptx":
		return parsePPTX(path)
	default:
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		data, err := readDocumentPart(f)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

// ConvertToPDF converts a file to PDF using LibreOffice headless mode.
// The resulting PDF is written to outDir and its path is returned.
func ConvertToPDF(path, outDir string) (string, error) {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", err
	}
	snapshot, cleanup, err := documentSnapshot(path)
	if err != nil {
		return "", err
	}
	defer cleanup()
	if err := checkDocument(snapshot); err != nil {
		return "", err
	}
	if _, err := runDocumentCommand("libreoffice", "--headless", "--convert-to", "pdf", "--outdir", outDir, snapshot); err != nil {
		return "", fmt.Errorf("libreoffice: %w (is libreoffice installed?)", err)
	}
	base := strings.TrimSuffix(filepath.Base(snapshot), filepath.Ext(snapshot))
	return filepath.Join(outDir, base+".pdf"), nil
}

// parsePPTX extracts text from all slides in a PPTX file, one slide per paragraph block.
func parsePPTX(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open pptx: %w", err)
	}
	defer r.Close()

	// Collect slide filenames and sort by slide number.
	var slideNames []string
	for _, f := range r.File {
		name := f.Name
		if strings.HasPrefix(name, "ppt/slides/slide") &&
			strings.HasSuffix(name, ".xml") &&
			!strings.Contains(name, "_rels") {
			slideNames = append(slideNames, name)
		}
	}
	sort.Slice(slideNames, func(i, j int) bool {
		return pptxSlideNum(slideNames[i]) < pptxSlideNum(slideNames[j])
	})

	fileMap := make(map[string]*zip.File, len(r.File))
	for _, f := range r.File {
		fileMap[f.Name] = f
	}

	var sb strings.Builder
	for _, name := range slideNames {
		f := fileMap[name]
		if f == nil {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := readDocumentPart(rc)
		rc.Close()
		if err != nil {
			continue
		}
		text, err := extractPPTXText(data)
		if err != nil {
			return "", fmt.Errorf("slide %s could not be read in full: %w", name, err)
		}
		if strings.TrimSpace(text) != "" {
			sb.WriteString(text)
			sb.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

func pptxSlideNum(name string) int {
	base := strings.TrimSuffix(filepath.Base(name), ".xml")
	base = strings.TrimPrefix(base, "slide")
	n, _ := strconv.Atoi(base)
	return n
}

// extractPPTXText pulls text runs (<a:t>) from a DrawingML slide XML. A parse
// error is reported rather than swallowed: the loop used to stop on any error,
// including a malformed document, and the caller indexed the partial text into
// the knowledge base as if it were the whole slide.
func extractPPTXText(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var sb strings.Builder
	inT := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return sb.String(), err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inT = true
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inT = false
			}
			if t.Name.Local == "p" {
				sb.WriteByte('\n')
			}
		case xml.CharData:
			if inT {
				sb.Write(t)
			}
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// hyphenBreak matches a word split across two lines by the typesetter: a letter,
// the typographic hyphen U+2010 that poppler emits, then the line break. Only
// this shape is a hyphenation — "real‐time" or "user‐defined" keep their hyphen
// because no newline follows. Half the chunks of a 183-page manual carried a
// broken word ("refer‐\nence"), which no search can ever match.
var hyphenBreak = regexp.MustCompile(`(\p{L})\x{2010}[ \t]*\r?\n[ \t]*(\p{Ll})`)

// typographic maps the characters poppler carries over from the PDF to their
// plain-text equivalents, so a query typed on a keyboard can match the text.
var typographic = strings.NewReplacer(
	"\u2010", "-", // remaining hyphens (compound words)
	"\u00ad", "", // soft hyphen: invisible, never wanted
	"\u2018", "'", "\u2019", "'",
	"\u201c", `"`, "\u201d", `"`,
	"\u00a0", " ", // non-breaking space
	"\ufb01", "fi", "\ufb02", "fl", "\ufb00", "ff", "\ufb03", "ffi", "\ufb04", "ffl",
)

// NormalizeExtractedText repairs text coming out of pdftotext: it rejoins words
// broken by end-of-line hyphenation, then normalises typographic characters.
// Order matters — the hyphenation pass must run before U+2010 is rewritten to a
// plain hyphen, otherwise a line break would be all that distinguishes the two.
func NormalizeExtractedText(s string) string {
	s = hyphenBreak.ReplaceAllString(s, "$1$2")
	return typographic.Replace(s)
}

func parsePDF(path string) (string, error) {
	out, err := runDocumentCommand("pdftotext", path, "-")
	if err != nil {
		return "", fmt.Errorf("pdftotext: %w (is poppler-utils installed?)", err)
	}
	return string(out), nil
}

// ParsePDFPages splits a PDF into per-page text (index 0 = page 1).
// pdftotext separates pages with a form-feed character (\x0c).
func ParsePDFPages(path string) ([]string, error) {
	out, err := runDocumentCommand("pdftotext", "-enc", "UTF-8", path, "-")
	if err != nil {
		return nil, fmt.Errorf("pdftotext: %w (is poppler-utils installed?)", err)
	}
	pages := strings.Split(NormalizeExtractedText(string(out)), "\x0c")
	for len(pages) > 0 && strings.TrimSpace(pages[len(pages)-1]) == "" {
		pages = pages[:len(pages)-1]
	}
	return pages, nil
}

// parseDOCX reads a .docx file (ZIP archive) and extracts text from
// word/document.xml without any external dependency.
func parseDOCX(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()

		data, err := readDocumentPart(rc)
		if err != nil {
			return "", err
		}
		text, err := extractDocxText(data)
		if err != nil {
			return "", fmt.Errorf("document.xml could not be read in full: %w", err)
		}
		return text, nil
	}
	return "", fmt.Errorf("word/document.xml not found in docx archive")
}

// parseXLSX reads an Excel workbook and renders each sheet as a CSV-like table
// with a header line, separated by blank lines. Formula cells emit their
// computed value, not the formula itself. Images and charts are ignored.
func parseXLSX(path string) (string, error) {
	f, err := excelize.OpenFile(path, excelize.Options{UnzipSizeLimit: maxDocumentBytes, UnzipXMLSizeLimit: maxDocumentBytes})
	if err != nil {
		return "", fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()

	var sb strings.Builder
	for _, sheet := range f.GetSheetList() {
		rows, err := f.Rows(sheet)
		if err != nil {
			return "", fmt.Errorf("read sheet %q: %w", sheet, err)
		}

		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString("Sheet: ")
		sb.WriteString(sheet)
		sb.WriteByte('\n')
		count := 0
		for rows.Next() {
			count++
			if count > 100000 {
				rows.Close()
				return "", errors.New("worksheet exceeds 100000 rows")
			}
			row, err := rows.Columns()
			if err != nil {
				rows.Close()
				return "", fmt.Errorf("invalid worksheet: %w", err)
			}
			line := strings.Join(row, ",")
			if sb.Len()+len(line)+1 > maxExtractedBytes {
				rows.Close()
				return "", errors.New("document text exceeds 8 MiB")
			}
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
		err = rows.Error()
		rows.Close()
		if err != nil {
			return "", err
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// extractDocxText parses the OOXML document.xml and collects text runs (<w:t>),
// inserting newlines at paragraph boundaries (<w:p>).
func extractDocxText(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var sb strings.Builder
	inT := false // inside <w:t>

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Truncated text indexed as if complete makes the agent answer
			// from half a document without knowing it.
			return sb.String(), err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inT = true
			case "p":
				sb.WriteByte('\n')
			case "br":
				sb.WriteByte('\n')
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inT = false
			}
		case xml.CharData:
			if inT {
				sb.Write(t)
			}
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// Bound expanded input before handing OOXML to a library. Validate sparse
// spreadsheet indices too: a tiny XML file can otherwise allocate a huge grid.
func checkDocument(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxDocumentBytes {
		return errors.New("document exceeds 16 MiB or is not a regular file")
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".xlsx", ".docx", ".pptx":
	default:
		return nil
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer z.Close()
	if len(z.File) > 2048 {
		return errors.New("too many document archive entries")
	}
	var total uint64
	for _, f := range z.File {
		if f.UncompressedSize64 > maxDocumentBytes || total > maxDocumentBytes-f.UncompressedSize64 {
			return errors.New("expanded document exceeds 16 MiB")
		}
		total += f.UncompressedSize64
		if !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		data, err := readDocumentPart(rc)
		rc.Close()
		if err != nil {
			return err
		}
		dec := xml.NewDecoder(bytes.NewReader(data))
		depth, nodes := 0, 0
		sharedCell, value := false, false
		var index strings.Builder
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "c" {
					sharedCell = false
					for _, a := range t.Attr {
						if a.Name.Local == "t" && a.Value == "s" {
							sharedCell = true
						}
					}
				}
				if t.Name.Local == "v" && sharedCell {
					value = true
					index.Reset()
				}
				depth++
				nodes++
				if depth > 128 || nodes > 200000 {
					return errors.New("document XML is too complex")
				}
				if strings.HasPrefix(f.Name, "xl/worksheets/") {
					for _, a := range t.Attr {
						if a.Name.Local != "r" {
							continue
						}
						if t.Name.Local == "row" {
							n, err := strconv.Atoi(a.Value)
							if err != nil || n < 1 || n > 100000 {
								return errors.New("worksheet row outside supported range")
							}
						}
						if t.Name.Local == "c" {
							col, row, err := excelize.CellNameToCoordinates(a.Value)
							if err != nil || col > 4096 || row > 100000 {
								return errors.New("worksheet cell outside supported range")
							}
						}
					}
				}
			case xml.CharData:
				if value {
					index.Write(t)
				}
			case xml.EndElement:
				if t.Name.Local == "v" && value {
					n, err := strconv.Atoi(strings.TrimSpace(index.String()))
					if err != nil || n < 0 || n > 200000 {
						return errors.New("invalid shared-string index")
					}
					value = false
				}
				if t.Name.Local == "c" {
					sharedCell = false
				}
				depth--
			}
		}
	}
	return nil
}
func readDocumentPart(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxDocumentBytes+1))
	if len(b) > maxDocumentBytes {
		return nil, errors.New("document part exceeds 16 MiB")
	}
	return b, err
}

type documentOutput struct{ bytes.Buffer }

func (b *documentOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxExtractedBytes {
		return 0, errors.New("extracted text exceeds 8 MiB")
	}
	return b.Buffer.Write(p)
}
func runDocumentCommand(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Poppler parses untrusted data outside the Go process. Bound child memory
	// and CPU where Linux prlimit is available, as well as output and wall time.
	if limit, err := exec.LookPath("prlimit"); err == nil {
		args = append([]string{"--as=268435456", "--cpu=20", "--", name}, args...)
		name = limit
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out := &documentOutput{}
	cmd.Stdout = out
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	return out.Bytes(), err
}

// Snapshot before validation: agent code may concurrently rewrite its files.
func documentSnapshot(path string) (string, func(), error) {
	src, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxDocumentBytes {
		return "", nil, errors.New("document exceeds 16 MiB or is not regular")
	}
	dst, err := os.CreateTemp("", "prism-document-*"+filepath.Ext(path))
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.Remove(dst.Name()) }
	n, err := io.Copy(dst, io.LimitReader(src, maxDocumentBytes+1))
	closeErr := dst.Close()
	if err != nil || closeErr != nil || n > maxDocumentBytes {
		cleanup()
		return "", nil, errors.New("document snapshot failed or exceeds 16 MiB")
	}
	return dst.Name(), cleanup, nil
}
