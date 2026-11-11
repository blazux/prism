package customtools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeaderValidationMatchesDiscovery(t *testing.T) {
	for _, test := range []struct {
		name, header string
		valid        bool
	}{
		{"legacy no args", `{"name":"example"}`, true},
		{"typed args", `{"name":"example","parameters":{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"]}}`, true},
		{"wrong parameters type", `{"name":"example","parameters":"object"}`, false},
		{"wrong description type", `{"name":"example","description":[]}`, false},
		{"nonobject args", `{"name":"example","parameters":{"type":"string"}}`, false},
		{"missing required property", `{"name":"example","parameters":{"required":["missing"]}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			code := "# TOOL: " + test.header + "\nprint('ok')\n"
			tool, err := ParseHeader(strings.NewReader(code))
			if (err == nil) != test.valid {
				t.Fatalf("ParseHeader error=%v, valid=%v", err, test.valid)
			}
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "example.py"), []byte(code), 0600); err != nil {
				t.Fatal(err)
			}
			loaded := LoadManager(root).Get("example")
			if (loaded != nil) != test.valid {
				t.Fatalf("discovery=%v, valid=%v", loaded, test.valid)
			}
			if loaded != nil && (loaded.Parameters.Type != "object" || loaded.Name != tool.Name) {
				t.Fatalf("discovery differs: %#v", loaded)
			}
		})
	}
	code := "# TOOL: {\"name\":\"broken\",}\n# TOOL: {\"name\":\"valid\"}\n"
	if tool, err := ParseHeader(strings.NewReader(code)); err != nil || tool.Name != "valid" {
		t.Fatalf("later valid header: %#v %v", tool, err)
	}
	for _, line := range []int{30, 31} {
		code := strings.Repeat("# comment\n", line-1) + "# TOOL: {\"name\":\"edge\"}\n"
		_, err := ParseHeader(strings.NewReader(code))
		if (err == nil) != (line == 30) {
			t.Fatalf("line %d: %v", line, err)
		}
	}
}

func TestInPlaceMetadataEditRefreshesEveryRegistryView(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "example.py")
	original := "# TOOL: {\"name\":\"before\",\"description\":\"first\"}\n"
	updated := "# TOOL: {\"name\":\"after_\",\"description\":\"other\"}\n"
	if len(original) != len(updated) {
		t.Fatal("fixture must retain size")
	}
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	m := LoadManager(root)
	before, _ := os.Stat(root)
	info, _ := os.Stat(path)
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now(), info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(root)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("fixture changed directory mtime")
	}
	if m.Get("before") != nil || m.Get("after_") == nil {
		t.Fatal("in-place rename not refreshed")
	}
	if got := m.ToOllamaTools(); len(got) != 1 || got[0].Function.Description != "other" {
		t.Fatalf("stale catalog: %#v", got)
	}
	if err := os.WriteFile(path, []byte("# TOOL: {\"name\":\"after_\",\"protected\":true}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !m.IsProtectedFilename("example.py") {
		t.Fatal("in-place protection change not refreshed")
	}
}

func TestInvalidFileFixedInPlaceBecomesDiscoverable(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "example.py")
	if err := os.WriteFile(path, []byte("# TOOL: broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := LoadManager(root)
	if err := os.WriteFile(path, []byte("# TOOL: {\"name\":\"fixed\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if m.Get("fixed") == nil {
		t.Fatal("fixed metadata remains invisible")
	}
}
