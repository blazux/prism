package notes

// VaultProvider reads and writes notes as Markdown files in a directory — an
// Obsidian or Logseq vault. A note's title is its filename (sans .md), its body
// is the file's raw content (frontmatter preserved verbatim, so round-trips are
// lossless), and its tags are parsed best-effort from YAML frontmatter. The id
// is the vault-relative path, which keeps [[wikilinks]] meaningful.

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type VaultProvider struct{ Dir string }

func (p *VaultProvider) Kind() string { return "vault" }

const vaultMaxFiles = 5000

func (p *VaultProvider) List(ctx context.Context) ([]Item, error) { return p.listWithRecovery(ctx) }
func (p *VaultProvider) Save(ctx context.Context, id, title, body, tags string) (string, error) {
	return p.saveWithRecovery(ctx, id, title, body, tags)
}

func (p *VaultProvider) Delete(ctx context.Context, id string) error {
	return p.deleteWithRecovery(ctx, id)
}

// ─── helpers ────────────────────────────────────────────────────────────────────

func itemFrom(rel, raw string, mod time.Time) Item {
	title := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	tags := ""
	if meta, ok := parseFrontmatter(raw); ok {
		if t := frontmatterTitle(meta["title"]); t != "" {
			title = t
		}
		if tg := meta["tags"]; tg != "" {
			tags = normTags(tg)
		}
	}
	return Item{ID: filepath.ToSlash(rel), Title: title, Body: raw, Tags: tags, CreatedAt: mod, UpdatedAt: mod}
}

func frontmatterTitle(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if decoded, err := strconv.Unquote(value); err == nil {
			return decoded
		}
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	return value
}

// Only an explicit title-field edit updates an existing flat frontmatter title.
// Ordinary body saves keep every byte, and all unrelated metadata/line endings
// remain untouched even during a rename.
func replaceFrontmatterTitle(raw, title string) string {
	if _, ok := parseFrontmatter(raw); !ok {
		return raw
	}
	lines := strings.SplitAfter(raw, "\n")
	index, colon := -1, 0
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSuffix(strings.TrimSuffix(lines[i], "\n"), "\r")
		if strings.TrimSpace(line) == "---" {
			if index < 0 {
				return raw
			}
			old := lines[index]
			ending := ""
			if strings.HasSuffix(old, "\r\n") {
				ending = "\r\n"
			} else if strings.HasSuffix(old, "\n") {
				ending = "\n"
			}
			content := strings.TrimSuffix(old, ending)
			value := content[colon+1:]
			leading := value[:len(value)-len(strings.TrimLeft(value, " \t"))]
			if leading == "" {
				leading = " "
			}
			trailing := value[len(strings.TrimRight(value, " \t")):]
			lines[index] = content[:colon+1] + leading + strconv.Quote(title) + trailing + ending
			return strings.Join(lines, "")
		}
		at := strings.IndexByte(line, ':')
		if at > 0 && strings.EqualFold(strings.TrimSpace(line[:at]), "title") {
			index, colon = i, at
		}
	}
	return raw
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
