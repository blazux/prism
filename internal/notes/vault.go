package notes

// VaultProvider reads and writes notes as Markdown files in a directory — an
// Obsidian or Logseq vault. A note's title is its filename (sans .md), its body
// is the file's raw content (frontmatter preserved verbatim, so round-trips are
// lossless), and its tags are parsed best-effort from YAML frontmatter. The id
// is the vault-relative path, which keeps [[wikilinks]] meaningful.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type VaultProvider struct{ Dir string }

func (p *VaultProvider) Kind() string { return "vault" }

const vaultMaxFiles = 5000

func (p *VaultProvider) List(ctx context.Context) ([]Item, error) {
	var items []Item
	err := filepath.WalkDir(p.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			// Skip dotfolders (.obsidian, .git, .trash, …) but not the root.
			if path != p.Dir && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			return nil
		}
		if len(items) >= vaultMaxFiles {
			return filepath.SkipAll
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(p.Dir, path)
		info, _ := d.Info()
		mod := time.Now()
		if info != nil {
			mod = info.ModTime()
		}
		items = append(items, itemFrom(rel, string(raw), mod))
		return nil
	})
	return items, err
}

func (p *VaultProvider) Save(ctx context.Context, id, title, body, tags string) (string, error) {
	if id == "" { // create
		name := sanitizeFilename(title)
		if name == "" {
			name = "Untitled"
		}
		full := uniquePath(filepath.Join(p.Dir, name+".md"))
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			return "", err
		}
		return relSlash(p.Dir, full), nil
	}
	full, err := p.resolve(id)
	if err != nil {
		return "", err
	}
	// Rename the file when the title (i.e. the note name) changed.
	target := full
	cur := strings.TrimSuffix(filepath.Base(full), filepath.Ext(full))
	if nn := sanitizeFilename(title); nn != "" && !strings.EqualFold(nn, cur) {
		target = uniquePath(filepath.Join(filepath.Dir(full), nn+".md"))
	}
	if err := os.WriteFile(target, []byte(body), 0644); err != nil {
		return "", err
	}
	if target != full {
		_ = os.Remove(full)
	}
	return relSlash(p.Dir, target), nil
}

func (p *VaultProvider) Delete(ctx context.Context, id string) error {
	full, err := p.resolve(id)
	if err != nil {
		return err
	}
	return os.Remove(full)
}

// ─── helpers ────────────────────────────────────────────────────────────────────

// resolve turns a client-supplied id (vault-relative path) into an absolute path,
// rejecting anything that escapes the vault or isn't a .md file.
func (p *VaultProvider) resolve(id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("empty id")
	}
	full := filepath.Join(p.Dir, filepath.Clean(filepath.FromSlash(id)))
	rel, err := filepath.Rel(p.Dir, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes vault")
	}
	if !strings.HasSuffix(strings.ToLower(full), ".md") {
		return "", fmt.Errorf("not a markdown file")
	}
	return full, nil
}

func relSlash(base, full string) string {
	rel, _ := filepath.Rel(base, full)
	return filepath.ToSlash(rel)
}

func itemFrom(rel, raw string, mod time.Time) Item {
	title := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	tags := ""
	if meta, ok := parseFrontmatter(raw); ok {
		if t := strings.Trim(meta["title"], `'"`); t != "" {
			title = t
		}
		if tg := meta["tags"]; tg != "" {
			tags = normTags(tg)
		}
	}
	return Item{ID: filepath.ToSlash(rel), Title: title, Body: raw, Tags: tags, CreatedAt: mod, UpdatedAt: mod}
}

// parseFrontmatter does a minimal scan of a leading `---` YAML block, returning
// flat key→value pairs (good enough for title/tags; not a full YAML parser).
func parseFrontmatter(raw string) (map[string]string, bool) {
	if !strings.HasPrefix(raw, "---\n") && !strings.HasPrefix(raw, "---\r\n") {
		return nil, false
	}
	rest := raw[strings.IndexByte(raw, '\n')+1:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, false
	}
	meta := map[string]string{}
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimRight(line, "\r")
		i := strings.IndexByte(line, ':')
		if i <= 0 {
			continue
		}
		meta[strings.ToLower(strings.TrimSpace(line[:i]))] = strings.TrimSpace(line[i+1:])
	}
	return meta, true
}

// normTags normalises inline frontmatter tags ("[a, b]" or "a, b") to "a, b".
func normTags(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(strings.Trim(p, `'"#`)); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

func sanitizeFilename(s string) string {
	s = strings.NewReplacer("/", "-", "\\", "-", ":", "-", "\n", " ", "\r", " ", "\x00", "", "\"", "'").Replace(s)
	s = strings.Trim(strings.TrimSpace(s), ". ")
	if len(s) > 120 {
		s = strings.TrimSpace(s[:120])
	}
	return s
}

// uniquePath appends " 2", " 3", … before the extension until the path is free.
func uniquePath(full string) string {
	if _, err := os.Stat(full); os.IsNotExist(err) {
		return full
	}
	ext := filepath.Ext(full)
	stem := strings.TrimSuffix(full, ext)
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s %d%s", stem, i, ext)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}
