package notes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const vaultRecoveryDir = ".prism-notes"

var vaultLocks sync.Map

func (p *VaultProvider) lock() func() {
	dir, _ := filepath.Abs(p.Dir)
	v, _ := vaultLocks.LoadOrStore(dir, new(sync.Mutex))
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

type vaultEntry struct {
	Key         string     `json:"key"`
	Path        string     `json:"path"`
	Aliases     []string   `json:"aliases,omitempty"`
	Versions    []string   `json:"versions"`
	VersionMeta []Version  `json:"versionMeta"`
	DeletedAt   *time.Time `json:"deletedAt,omitempty"`
}
type vaultIndex struct {
	Entries []*vaultEntry `json:"entries"`
}
type vaultSnapshot struct {
	Version
	Path string `json:"path"`
}

func token() string {
	var b [16]byte
	_, err := rand.Read(b[:])
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func validVaultID(id string) error {
	if id == "" || strings.Contains(id, "\\") || strings.ContainsRune(id, 0) || strings.HasPrefix(id, "/") || path.Clean(id) != id || !strings.HasSuffix(strings.ToLower(id), ".md") {
		return fmt.Errorf("invalid Markdown note path")
	}
	for _, c := range strings.Split(id, "/") {
		if strings.HasPrefix(c, ".") {
			return fmt.Errorf("hidden paths and traversal are not note ids")
		}
	}
	return nil
}
func vaultNoSymlinks(root *os.Root, id string) error {
	parts := strings.Split(id, "/")
	for i := range parts {
		info, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links cannot be used for note storage")
		}
	}
	return nil
}
func loadVaultIndex(root *os.Root) (*vaultIndex, error) {
	if err := vaultNoSymlinks(root, vaultRecoveryDir+"/index.json"); err != nil {
		return nil, err
	}
	raw, err := root.ReadFile(vaultRecoveryDir + "/index.json")
	if errors.Is(err, os.ErrNotExist) {
		return &vaultIndex{Entries: []*vaultEntry{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var idx vaultIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("could not read note recovery metadata: %w", err)
	}
	return &idx, nil
}
func rootAtomicWrite(root *os.Root, name string, data []byte, exclusive bool) error {
	if err := vaultNoSymlinks(root, name); err != nil {
		return err
	}
	tmp := path.Join(path.Dir(name), ".prism-"+token()+".tmp")
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if exclusive {
		return root.Link(tmp, name)
	}
	return root.Rename(tmp, name)
}
func saveVaultIndex(root *os.Root, idx *vaultIndex) error {
	if err := vaultNoSymlinks(root, vaultRecoveryDir+"/versions"); err != nil {
		return err
	}
	if err := root.MkdirAll(vaultRecoveryDir+"/versions", 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	return rootAtomicWrite(root, vaultRecoveryDir+"/index.json", raw, false)
}
func findVaultEntry(idx *vaultIndex, id string) *vaultEntry {
	if strings.HasPrefix(id, "trash:") {
		key := strings.TrimPrefix(id, "trash:")
		for _, e := range idx.Entries {
			if e.Key == key {
				return e
			}
		}
		return nil
	}
	for _, e := range idx.Entries {
		if e.Path == id && e.DeletedAt == nil {
			return e
		}
	}
	var exact *vaultEntry
	for _, e := range idx.Entries {
		if e.Path == id {
			if exact != nil {
				return nil
			}
			exact = e
		}
	}
	if exact != nil {
		return exact
	}
	var found *vaultEntry
	for _, e := range idx.Entries {
		for _, a := range e.Aliases {
			if a == id {
				if found != nil && found != e {
					return nil
				}
				found = e
			}
		}
	}
	return found
}
func addAliases(e *vaultEntry, values ...string) {
	for _, v := range values {
		if v == "" {
			continue
		}
		found := false
		for _, a := range e.Aliases {
			found = found || a == v
		}
		if !found {
			e.Aliases = append(e.Aliases, v)
		}
	}
}
func entryForFile(idx *vaultIndex, id string) *vaultEntry {
	for _, e := range idx.Entries {
		if e.Path == id && e.DeletedAt == nil {
			return e
		}
	}
	e := &vaultEntry{Key: token(), Path: id, Versions: []string{}}
	idx.Entries = append(idx.Entries, e)
	return e
}
func resolveVaultEntry(root *os.Root, idx *vaultIndex, id string) (string, *vaultEntry, error) {
	if err := validVaultID(id); err != nil {
		return "", nil, err
	}
	actual := id
	if err := vaultNoSymlinks(root, id); err != nil {
		return "", nil, err
	}
	if _, err := root.Stat(id); errors.Is(err, os.ErrNotExist) {
		if e := findVaultEntry(idx, id); e != nil {
			actual = e.Path
		}
	} else if err != nil {
		return "", nil, err
	}
	if err := validVaultID(actual); err != nil {
		return "", nil, err
	}
	if err := vaultNoSymlinks(root, actual); err != nil {
		return "", nil, err
	}
	return actual, findVaultEntry(idx, actual), nil
}
func snapshotVault(root *os.Root, idx *vaultIndex, e *vaultEntry, raw []byte, mod time.Time) error {
	item := itemFrom(e.Path, string(raw), mod)
	v := vaultSnapshot{Version: Version{ID: token(), Title: item.Title, Body: string(raw), Tags: item.Tags, CreatedAt: mod}, Path: e.Path}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := vaultNoSymlinks(root, vaultRecoveryDir+"/versions"); err != nil {
		return err
	}
	if err := root.MkdirAll(vaultRecoveryDir+"/versions", 0700); err != nil {
		return err
	}
	if err := rootAtomicWrite(root, vaultRecoveryDir+"/versions/"+v.ID+".json", data, true); err != nil {
		return err
	}
	e.Versions = append(e.Versions, v.ID)
	meta := v.Version
	meta.Body = ""
	e.VersionMeta = append(e.VersionMeta, meta)
	return saveVaultIndex(root, idx)
}
func loadVaultVersion(root *os.Root, e *vaultEntry, id string) (vaultSnapshot, error) {
	allowed := false
	for _, v := range e.Versions {
		allowed = allowed || id == v
	}
	if !allowed {
		return vaultSnapshot{}, os.ErrNotExist
	}
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		return vaultSnapshot{}, fmt.Errorf("invalid note version")
	}
	if err := vaultNoSymlinks(root, vaultRecoveryDir+"/versions/"+id+".json"); err != nil {
		return vaultSnapshot{}, err
	}
	raw, err := root.ReadFile(vaultRecoveryDir + "/versions/" + id + ".json")
	if err != nil {
		return vaultSnapshot{}, err
	}
	var v vaultSnapshot
	err = json.Unmarshal(raw, &v)
	return v, err
}

func (p *VaultProvider) listWithRecovery(ctx context.Context) ([]Item, error) {
	unlock := p.lock()
	defer unlock()
	root, err := os.OpenRoot(p.Dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	idx, err := loadVaultIndex(root)
	if err != nil {
		return nil, err
	}
	items := []Item{}
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if name != "." && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(strings.ToLower(name), ".md") {
			return nil
		}
		if len(items) >= vaultMaxFiles {
			return fs.SkipAll
		}
		raw, err := root.ReadFile(name)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		item := itemFrom(name, string(raw), info.ModTime())
		item.Aliases = []string{strings.TrimSuffix(name, path.Ext(name))}
		if e := findVaultEntry(idx, name); e != nil && e.Path == name && e.DeletedAt == nil {
			item.Aliases = append(item.Aliases, e.Aliases...)
		}
		items = append(items, item)
		return nil
	})
	return items, err
}

func (p *VaultProvider) saveWithRecovery(ctx context.Context, id, title, body, tags string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if want := normTags(tags); want != "" && !strings.EqualFold(want, itemFrom(id, body, time.Time{}).Tags) {
		return "", fmt.Errorf("this note lives in your Markdown vault, where tags belong to the note itself: put `tags: %s` in the YAML front-matter at the top of the body", want)
	}
	unlock := p.lock()
	defer unlock()
	root, err := os.OpenRoot(p.Dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	idx, err := loadVaultIndex(root)
	if err != nil {
		return "", err
	}
	if id == "" {
		name := sanitizeFilename(title)
		if name == "" {
			name = "Untitled"
		}
		target, err := uniqueVaultPath(root, idx, name+".md")
		if err != nil {
			return "", err
		}
		if err := rootAtomicWrite(root, target, []byte(body), true); err != nil {
			return "", err
		}
		return target, nil
	}
	actual, _, err := resolveVaultEntry(root, idx, id)
	if err != nil {
		return "", err
	}
	raw, err := root.ReadFile(actual)
	if err != nil {
		return "", err
	}
	info, err := root.Stat(actual)
	if err != nil {
		return "", err
	}
	old := itemFrom(actual, string(raw), info.ModTime())
	if title != old.Title {
		if meta, ok := parseFrontmatter(body); ok && (strings.HasPrefix(meta["title"], "|") || strings.HasPrefix(meta["title"], ">")) {
			return "", fmt.Errorf("this note uses a multiline YAML title; edit that title in the Markdown body before renaming it")
		}
		body = replaceFrontmatterTitle(body, title)
	}
	target := actual
	newName := sanitizeFilename(title)
	base := strings.TrimSuffix(path.Base(actual), path.Ext(actual))
	if newName != "" && title != old.Title && newName != base {
		target, err = uniqueVaultPath(root, idx, path.Join(path.Dir(actual), newName+".md"))
		if err != nil {
			return "", err
		}
	}
	if target == actual && string(raw) == body {
		return actual, nil
	}
	e := entryForFile(idx, actual)
	if err := snapshotVault(root, idx, e, raw, info.ModTime()); err != nil {
		return "", err
	}
	if err := rootAtomicWrite(root, target, []byte(body), target != actual); err != nil {
		return "", err
	}
	if target != actual {
		addAliases(e, actual, strings.TrimSuffix(actual, path.Ext(actual)), old.Title)
		e.Path = target
		if err := saveVaultIndex(root, idx); err != nil {
			return "", err
		}
		if err := root.Remove(actual); err != nil {
			return "", fmt.Errorf("renamed note was saved but the original could not be removed: %w", err)
		}
	} else if old.Title != itemFrom(target, body, time.Time{}).Title {
		addAliases(e, old.Title)
		if err := saveVaultIndex(root, idx); err != nil {
			return "", err
		}
	}
	return target, nil
}
func uniqueVaultPath(root *os.Root, idx *vaultIndex, name string) (string, error) {
	if err := validVaultID(name); err != nil {
		return "", err
	}
	for i := 1; i < 100000; i++ {
		candidate := name
		if i > 1 {
			candidate = strings.TrimSuffix(name, path.Ext(name)) + fmt.Sprintf(" %d", i) + path.Ext(name)
		}
		reserved := false
		for _, e := range idx.Entries {
			if e.Path == candidate {
				reserved = true
				break
			}
			for _, alias := range e.Aliases {
				if alias == candidate {
					reserved = true
					break
				}
			}
		}
		if reserved {
			continue
		}
		if err := vaultNoSymlinks(root, candidate); err != nil {
			return "", err
		}
		_, err := root.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("could not choose an unused note filename")
}
func (p *VaultProvider) deleteWithRecovery(ctx context.Context, id string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	unlock := p.lock()
	defer unlock()
	root, err := os.OpenRoot(p.Dir)
	if err != nil {
		return err
	}
	defer root.Close()
	idx, err := loadVaultIndex(root)
	if err != nil {
		return err
	}
	actual, _, err := resolveVaultEntry(root, idx, id)
	if err != nil {
		return err
	}
	raw, err := root.ReadFile(actual)
	if err != nil {
		return err
	}
	info, err := root.Stat(actual)
	if err != nil {
		return err
	}
	e := entryForFile(idx, actual)
	if err := snapshotVault(root, idx, e, raw, info.ModTime()); err != nil {
		return err
	}
	now := time.Now().UTC()
	e.DeletedAt = &now
	if err := saveVaultIndex(root, idx); err != nil {
		return err
	}
	if err := root.Remove(actual); err != nil {
		e.DeletedAt = nil
		_ = saveVaultIndex(root, idx)
		return err
	}
	return nil
}
func (p *VaultProvider) History(ctx context.Context, id, before string) ([]Version, error) {
	if !strings.HasPrefix(id, "trash:") {
		if err := validVaultID(id); err != nil {
			return nil, err
		}
	}
	unlock := p.lock()
	defer unlock()
	root, err := os.OpenRoot(p.Dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	idx, err := loadVaultIndex(root)
	if err != nil {
		return nil, err
	}
	e := findVaultEntry(idx, id)
	if e != nil && !strings.HasPrefix(id, "trash:") {
		if _, err := root.Stat(id); err == nil && (e.DeletedAt != nil || e.Path != id) {
			e = nil
		}
	}
	if e == nil {
		if err := vaultNoSymlinks(root, id); err != nil {
			return nil, err
		}
		if _, err := root.Stat(id); err != nil {
			return nil, err
		}
		return []Version{}, nil
	}
	out := []Version{}
	started := before == ""
	for i := len(e.VersionMeta) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		v := e.VersionMeta[i]
		if !started {
			if v.ID == before {
				started = true
			}
			continue
		}
		out = append(out, v)
		if len(out) >= 50 {
			break
		}
	}
	if !started {
		return nil, fmt.Errorf("invalid history cursor")
	}
	return out, nil
}
func (p *VaultProvider) ReadVersion(ctx context.Context, id, version string) (Version, error) {
	if !strings.HasPrefix(id, "trash:") {
		if err := validVaultID(id); err != nil {
			return Version{}, err
		}
	}
	unlock := p.lock()
	defer unlock()
	root, err := os.OpenRoot(p.Dir)
	if err != nil {
		return Version{}, err
	}
	defer root.Close()
	idx, err := loadVaultIndex(root)
	if err != nil {
		return Version{}, err
	}
	e := findVaultEntry(idx, id)
	if e != nil && !strings.HasPrefix(id, "trash:") {
		if _, err := root.Stat(id); err == nil && (e.DeletedAt != nil || e.Path != id) {
			e = nil
		}
	}
	if e == nil {
		return Version{}, os.ErrNotExist
	}
	v, err := loadVaultVersion(root, e, version)
	return v.Version, err
}
func (p *VaultProvider) Trash(ctx context.Context) ([]Item, error) {
	unlock := p.lock()
	defer unlock()
	root, err := os.OpenRoot(p.Dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	idx, err := loadVaultIndex(root)
	if err != nil {
		return nil, err
	}
	out := []Item{}
	for _, e := range idx.Entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if e.DeletedAt == nil || len(e.Versions) == 0 {
			continue
		}
		if len(e.VersionMeta) == 0 {
			return nil, fmt.Errorf("note recovery metadata is incomplete")
		}
		v := e.VersionMeta[len(e.VersionMeta)-1]
		item := Item{ID: "trash:" + e.Key, Title: v.Title, Tags: v.Tags, CreatedAt: v.CreatedAt, UpdatedAt: v.CreatedAt}
		item.ID = "trash:" + e.Key
		item.DeletedAt = e.DeletedAt
		item.Aliases = e.Aliases
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt.After(*out[j].DeletedAt) })
	return out, nil
}
func (p *VaultProvider) Restore(ctx context.Context, id, version string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if !strings.HasPrefix(id, "trash:") {
		if err := validVaultID(id); err != nil {
			return "", err
		}
	}
	unlock := p.lock()
	defer unlock()
	root, err := os.OpenRoot(p.Dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	idx, err := loadVaultIndex(root)
	if err != nil {
		return "", err
	}
	e := findVaultEntry(idx, id)
	if e != nil && !strings.HasPrefix(id, "trash:") && e.Path != id {
		if _, err := root.Stat(id); err == nil {
			e = nil
		}
	}
	if e == nil {
		return "", os.ErrNotExist
	}
	if err := validVaultID(e.Path); err != nil {
		return "", err
	}
	if err := vaultNoSymlinks(root, e.Path); err != nil {
		return "", err
	}
	explicitVersion := version != ""
	if version == "" {
		if e.DeletedAt == nil {
			return e.Path, nil
		}
		if len(e.Versions) == 0 {
			return "", os.ErrNotExist
		}
		version = e.Versions[len(e.Versions)-1]
	}
	v, err := loadVaultVersion(root, e, version)
	if err != nil {
		return "", err
	}
	current := e.Path
	target := current
	if explicitVersion {
		target = v.Path
	}
	if err := validVaultID(target); err != nil {
		return "", err
	}
	if err := vaultNoSymlinks(root, target); err != nil {
		return "", err
	}
	deleted := e.DeletedAt != nil
	if deleted || target != current {
		if _, err := root.Lstat(target); err == nil {
			return "", ErrRecoveryConflict
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if !deleted {
		raw, err := root.ReadFile(current)
		if err != nil {
			return "", err
		}
		info, err := root.Stat(current)
		if err != nil {
			return "", err
		}
		if err := snapshotVault(root, idx, e, raw, info.ModTime()); err != nil {
			return "", err
		}
		addAliases(e, itemFrom(current, string(raw), info.ModTime()).Title)
	}
	if err := root.MkdirAll(path.Dir(target), 0755); err != nil {
		return "", err
	}
	if err := rootAtomicWrite(root, target, []byte(v.Body), deleted || target != current); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", ErrRecoveryConflict
		}
		return "", err
	}
	if target != current {
		addAliases(e, current, strings.TrimSuffix(current, path.Ext(current)))
		e.Path = target
	}
	e.DeletedAt = nil
	if err := saveVaultIndex(root, idx); err != nil {
		return "", err
	}
	if !deleted && target != current {
		if err := root.Remove(current); err != nil {
			return "", err
		}
	}
	return target, nil
}
