package rag

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"
)

// ImageDir returns the directory where page images for a document are stored.
// Uses the sanitized filename (without extension) — no hash — so the agent can
// reference the path without risk of copy errors.
func ImageDir(workspaceDir, collection, filename string) string {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	safe := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, base)
	return filepath.Join(workspaceDir, "rag_images", collection, safe)
}

// PageImagePath returns the path for a specific page image.
func PageImagePath(workspaceDir, collection, filename string, page int) string {
	return filepath.Join(ImageDir(workspaceDir, collection, filename), fmt.Sprintf("page-%04d.jpg", page))
}

// ParseFile extracts plain text from a file based on its extension.
// Supported: .txt .md .csv .json (raw read), .pdf (pdftotext), .docx (zip+xml), .xlsx (excelize), .pptx (zip+xml).
func ParseFile(path string) (string, error) {
	text, err := parseByExt(path)
	if err != nil {
		return "", err
	}
	// Every format goes through the same repair: DOCX and TXT rarely hyphenate,
	// but they do carry curly quotes, non-breaking spaces and soft hyphens, which
	// a query typed on a keyboard will never match. The pass is idempotent.
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
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
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

// ConvertToPDF converts a file to PDF using LibreOffice headless mode.
// The resulting PDF is written to outDir and its path is returned.
func ConvertToPDF(path, outDir string) (string, error) {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", err
	}
	if err := exec.Command("libreoffice", "--headless", "--convert-to", "pdf", "--outdir", outDir, path).Run(); err != nil {
		return "", fmt.Errorf("libreoffice: %w (is libreoffice installed?)", err)
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return filepath.Join(outDir, base+".pdf"), nil
}

// ExtractDOCXImages extracts JPEG and PNG images embedded in a DOCX file.
// Images are saved to outDir as page-0001.jpg, page-0002.png, etc.
// Returns the number of images extracted.
func ExtractDOCXImages(path, outDir string) (int, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return 0, fmt.Errorf("open docx: %w", err)
	}
	defer r.Close()

	if err := os.MkdirAll(outDir, 0755); err != nil {
		return 0, err
	}

	var count int
	for _, f := range r.File {
		if !strings.HasPrefix(f.Name, "word/media/") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name))
		if ext == ".jpeg" {
			ext = ".jpg"
		}
		if ext != ".jpg" && ext != ".png" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		count++
		dst := filepath.Join(outDir, fmt.Sprintf("page-%04d%s", count, ext))
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return count, err
		}
	}
	return count, nil
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
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		text := extractPPTXText(data)
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

// extractPPTXText pulls text runs (<a:t>) from a DrawingML slide XML.
func extractPPTXText(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var sb strings.Builder
	inT := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
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
	return strings.TrimSpace(sb.String())
}

func parsePDF(path string) (string, error) {
	out, err := exec.Command("pdftotext", path, "-").Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext: %w (is poppler-utils installed?)", err)
	}
	return string(out), nil
}

// ParsePDFPages splits a PDF into per-page text (index 0 = page 1).
// pdftotext separates pages with a form-feed character (\x0c).
func ParsePDFPages(path string) ([]string, error) {
	out, err := exec.Command("pdftotext", "-enc", "UTF-8", path, "-").Output()
	if err != nil {
		return nil, fmt.Errorf("pdftotext: %w (is poppler-utils installed?)", err)
	}
	pages := strings.Split(NormalizeExtractedText(string(out)), "\x0c")
	for len(pages) > 0 && strings.TrimSpace(pages[len(pages)-1]) == "" {
		pages = pages[:len(pages)-1]
	}
	return pages, nil
}

// ExtractPageImages renders each PDF page as a JPEG into outDir using pdftoppm.
// Output files are named page-0001.jpg, page-0002.jpg, etc.
func ExtractPageImages(pdfPath, outDir string) error {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	tmp := filepath.Join(outDir, "p")
	if err := exec.Command("pdftoppm", "-jpeg", "-r", "120", pdfPath, tmp).Run(); err != nil {
		return fmt.Errorf("pdftoppm: %w (is poppler-utils installed?)", err)
	}
	matches, err := filepath.Glob(filepath.Join(outDir, "p-*.jpg"))
	if err != nil {
		return err
	}
	sort.Strings(matches)
	for i, src := range matches {
		dst := filepath.Join(outDir, fmt.Sprintf("page-%04d.jpg", i+1))
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("rename %s: %w", src, err)
		}
	}
	return nil
}

// execCommandOutput runs a command and returns its stdout as a string.
func execCommandOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
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

		data, err := io.ReadAll(rc)
		if err != nil {
			return "", err
		}
		return extractDocxText(data), nil
	}
	return "", fmt.Errorf("word/document.xml not found in docx archive")
}

// parseXLSX reads an Excel workbook and renders each sheet as a CSV-like table
// with a header line, separated by blank lines. Formula cells emit their
// computed value, not the formula itself. Images and charts are ignored.
func parseXLSX(path string) (string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return "", fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()

	var sb strings.Builder
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			return "", fmt.Errorf("read sheet %q: %w", sheet, err)
		}
		if len(rows) == 0 {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString("Sheet: ")
		sb.WriteString(sheet)
		sb.WriteByte('\n')
		for _, row := range rows {
			sb.WriteString(strings.Join(row, ","))
			sb.WriteByte('\n')
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// extractDocxText parses the OOXML document.xml and collects text runs (<w:t>),
// inserting newlines at paragraph boundaries (<w:p>).
func extractDocxText(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var sb strings.Builder
	inT := false // inside <w:t>

	for {
		tok, err := dec.Token()
		if err != nil {
			break
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
	return strings.TrimSpace(sb.String())
}
