package memory

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every schema statement must be safe to run against a database created by any
// earlier version: existing deployments upgrade by pulling a new image, and
// initSchema is the only migration mechanism. This pins the two rules that make
// that true — additive only, idempotent only — so a plain CREATE TABLE or
// ALTER TABLE ... ADD COLUMN without IF NOT EXISTS fails the build, not a
// user's `docker compose up`. See docs/UPGRADING.md.
func TestSchemaStatementsAreIdempotent(t *testing.T) {
	for _, path := range []string{"store.go", "../rag/store.go"} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		checkSchemaSource(t, path, string(src))
	}
}

var (
	createRe = regexp.MustCompile("CREATE (TABLE|INDEX|UNIQUE INDEX|EXTENSION)\\s+(IF NOT EXISTS)?")
	alterRe  = regexp.MustCompile("ALTER TABLE\\s+\\S+\\s+(ADD COLUMN\\s+(IF NOT EXISTS)?|DROP|RENAME|ALTER COLUMN)")
	dropRe   = regexp.MustCompile("\\bDROP (TABLE|COLUMN)\\b")
)

func checkSchemaSource(t *testing.T, path, src string) {
	for i, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if m := createRe.FindStringSubmatch(line); m != nil && m[2] == "" {
			t.Errorf("%s:%d: CREATE without IF NOT EXISTS: %s", path, i+1, strings.TrimSpace(line))
		}
		if m := alterRe.FindStringSubmatch(line); m != nil {
			if !strings.HasPrefix(strings.ToUpper(m[1]), "ADD COLUMN") || m[2] == "" {
				t.Errorf("%s:%d: only additive, idempotent ALTERs are allowed: %s", path, i+1, strings.TrimSpace(line))
			}
		}
		if dropRe.MatchString(line) {
			t.Errorf("%s:%d: destructive schema change: %s", path, i+1, strings.TrimSpace(line))
		}
	}
}
