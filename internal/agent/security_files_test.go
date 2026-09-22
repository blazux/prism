package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentFilesConfinedAndOAuthFiltered(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "fixture"), []byte("outside"), 0600)
	os.Symlink(outside, filepath.Join(root, "linked"))
	os.WriteFile(filepath.Join(root, ".secret_key"), []byte("key"), 0600)
	e := &ToolExecutor{workspaceDir: root}
	for _, path := range []string{"linked/fixture", ".secret_key"} {
		if _, err := e.readFile(path); err == nil {
			t.Fatal("read accepted", path)
		}
		if _, err := e.writeFile(path, "overwrite"); err == nil {
			t.Fatal("write accepted", path)
		}
	}
	env, err := scriptSecretsEnv(map[string]string{"oauth_google_token": "fixture", "oauth_microsoft_client_secret": "fixture", "MY_KEY": "mine"})
	if err != nil || len(env) != 1 || env["MY_KEY"] != "mine" {
		t.Fatalf("incorrect filtering: %v", err)
	}
}
