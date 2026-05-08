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
	"strings"
)

// ParseFile extracts plain text from a file based on its extension.
// Supported: .txt .md .csv .json (raw read), .pdf (pdftotext), .docx (zip+xml).
func ParseFile(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		return parsePDF(path)
	case ".docx":
		return parseDOCX(path)
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
