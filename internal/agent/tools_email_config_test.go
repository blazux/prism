package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmailConfigWithoutFieldsIsReadOnlyAndScoped(t *testing.T) {
	ctx := t.Context()
	store := resourceDatabase(t)
	executor := NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	executor.SetMemoryStore(store)
	executor.SetPersonalScope("u7")
	status := func(action string, fields map[string]interface{}) (string, map[string]interface{}) {
		t.Helper()
		args := map[string]interface{}{"action": action}
		for key, value := range fields {
			args[key] = value
		}
		out, err := executor.emailTool(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("read must return structured non-secret settings: %v", err)
		}
		if result["connection_tested"] != false {
			t.Fatalf("status must not claim a live connection test: %s", out)
		}
		for _, key := range []string{"password", "password_secret"} {
			if _, ok := result[key]; ok {
				t.Fatalf("secret field %q returned by status", key)
			}
		}
		return out, result
	}

	// Neither action spelling should create a row or a credential when read
	// before setup. Unrelated list arguments must not turn a read into a write.
	for _, action := range []string{"config", "setup"} {
		_, result := status(action, map[string]interface{}{"limit": float64(20)})
		if result["configured"] != false || result["has_password"] != false {
			t.Fatalf("empty account reported configured: %#v", result)
		}
		if _, exists, err := executor.userStore().GetConfig(ctx, emailConfigKey); err != nil || exists {
			t.Fatalf("read created mailbox settings: exists=%v err=%v", exists, err)
		}
		if _, exists, err := executor.userStore().GetSecret(ctx, emailPasswordSecret); err != nil || exists {
			t.Fatalf("read created a mailbox password: exists=%v err=%v", exists, err)
		}
	}

	const secret = "fixture-mail-password-never-return"
	const legacySecret = "legacy-secret-never-return"
	raw := ` {"imap_host":"unreachable.invalid","imap_port":993,"smtp_host":"unreachable.invalid","smtp_port":587,"user":"fixture@example.test","from":"sender@example.test","security":"starttls","insecure":true,"list_limit":120,"password":"` + legacySecret + `","extension":"preserve unchanged"} `
	if err := executor.userStore().SetConfig(ctx, emailConfigKey, raw); err != nil {
		t.Fatal(err)
	}
	if err := executor.userStore().SetSecret(ctx, emailPasswordSecret, secret); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"config", "setup"} {
		out, result := status(action, nil)
		if result["configured"] != true || result["has_password"] != true || result["imap_host"] != "unreachable.invalid" || result["list_limit"] != float64(120) {
			t.Fatalf("saved mailbox status incorrect: %s", out)
		}
		if strings.Contains(out, secret) || strings.Contains(out, legacySecret) {
			t.Fatal("status exposed a secret value")
		}
		if got, exists, err := executor.userStore().GetConfig(ctx, emailConfigKey); err != nil || !exists || got != raw {
			t.Fatalf("read modified persisted configuration: exists=%v err=%v", exists, err)
		}
		if got, exists, err := executor.userStore().GetSecret(ctx, emailPasswordSecret); err != nil || !exists || got != secret {
			t.Fatalf("read modified the saved password: exists=%v err=%v", exists, err)
		}
	}

	// Another account cannot discover this user's settings or credential.
	executor.SetPersonalScope("u8")
	out, result := status("config", nil)
	if result["configured"] != false || result["has_password"] != false || result["user"] != "" || strings.Contains(out, "unreachable.invalid") {
		t.Fatalf("another account's mailbox leaked into status: %s", out)
	}
	if _, exists, err := executor.userStore().GetConfig(ctx, emailConfigKey); err != nil || exists {
		t.Fatalf("reading another account created settings: exists=%v err=%v", exists, err)
	}
}

func TestEmailConfigWithFieldsStillUpdatesMailbox(t *testing.T) {
	ctx := t.Context()
	store := resourceDatabase(t)
	executor := NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	executor.SetMemoryStore(store)
	executor.SetPersonalScope("u7")
	if err := executor.userStore().SetConfig(ctx, emailConfigKey, `{"imap_host":"unreachable.invalid","imap_port":993,"smtp_host":"smtp.invalid","smtp_port":587,"user":"before@example.test","security":"starttls","insecure":true,"list_limit":120}`); err != nil {
		t.Fatal(err)
	}
	const password = "fixture-new-mail-password"
	if err := executor.userStore().SetSecret(ctx, "mail_setup_password", password); err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]interface{}{
		{"action": "config", "user": "after@example.test", "password_secret": "mail_setup_password"},
		{"action": "setup", "insecure": false},
	} {
		out, err := executor.emailTool(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(out, "Email configured") || strings.Contains(out, password) {
			t.Fatalf("configuration mutation contract changed or exposed a password: %q", out)
		}
	}
	raw, ok, err := executor.userStore().GetConfig(ctx, emailConfigKey)
	if err != nil || !ok {
		t.Fatalf("updated settings missing: exists=%v err=%v", ok, err)
	}
	var config emailStoredConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	if config.User != "after@example.test" || config.Insecure || config.IMAPHost != "unreachable.invalid" || config.SMTPHost != "smtp.invalid" || config.ListLimit != 120 || config.Security != "starttls" {
		t.Fatalf("partial update failed to merge/preserve saved fields: %+v", config)
	}
	if got, exists, err := executor.userStore().GetSecret(ctx, emailPasswordSecret); err != nil || !exists || got != password {
		t.Fatalf("password_secret update failed: exists=%v err=%v", exists, err)
	}
}
