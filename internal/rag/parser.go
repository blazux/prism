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
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

// ParseFile extracts plain text from a file based on its extension.
// Supported: .txt .md .csv .json (raw read), .pdf (pdftotext), .docx (zip+xml), .xlsx (excelize).
func ParseFile(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		return parsePDF(path)
	case ".docx":
		return parseDOCX(path)
	case ".xlsx":
		return parseXLSX(path)
	default:
		// txt, md, csv, json, etc. — read as-is
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
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
	pages := strings.Split(string(out), "\x0c")
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
