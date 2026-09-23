package rag

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archiveFixture(t *testing.T, name, xml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.xlsx")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, err := z.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte(xml)); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return p
}
func TestRejectHostileOfficeDocuments(t *testing.T) {
	for _, body := range []string{
		"<worksheet><row r=\"999999999\"/></worksheet>",
		"<worksheet><row r=\"1\"><c r=\"A1\" t=\"s\"><v>-1</v></c></row></worksheet>",
		"<worksheet><row r=\"1\"><c r=\"ZZZZ1\"/></row></worksheet>",
		strings.Repeat("<x>", 130) + strings.Repeat("</x>", 130),
	} {
		p := archiveFixture(t, "xl/worksheets/sheet1.xml", body)
		if err := checkDocument(p); err == nil {
			t.Fatalf("accepted hostile XML: %.120s", body)
		}
		if _, err := ParseFile(p); err == nil {
			t.Fatal("ParseFile accepted hostile archive")
		}
	}
}
func TestRejectExpandedArchive(t *testing.T) {
	p := archiveFixture(t, "word/document.xml", strings.Repeat(" ", maxDocumentBytes+1))
	if err := checkDocument(p); err == nil {
		t.Fatal("accepted oversized expanded document")
	}
}
func TestDocumentOutputLimit(t *testing.T) {
	out := &documentOutput{}
	if _, err := out.Write(make([]byte, maxExtractedBytes+1)); err == nil {
		t.Fatal("unbounded output")
	}
}
