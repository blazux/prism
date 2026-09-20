package memory

import (
	"errors"
	"testing"
)

func TestScriptSecretNames(t *testing.T) {
	for _, name := range []string{"MY_API_KEY", "my-key", "openai_key", "clé_api"} {
		if err := ValidateScriptSecretName(name); err != nil {
			t.Errorf("valid name %q: %v", name, err)
		}
	}
	for _, name := range []string{"", "___", "123", " api_key ", "u1:key", "prism_token", "Prism-Session", "PRISM_URL", "email_password", "EMAIL-PASSWORD", "caldav_password", "todoist_token", "webex_bot_token:g1", "mcp_oauth_access"} {
		if err := ValidateScriptSecretName(name); !errors.Is(err, ErrSecretName) {
			t.Errorf("invalid name %q: %v", name, err)
		}
	}
	for _, pair := range [][2]string{{"my-key", "my_key"}, {"API_KEY", "api_key"}, {"a.b", "a-b"}} {
		if !errors.Is(checkSecretAlias(pair[0], pair[1]), ErrSecretName) {
			t.Errorf("alias accepted: %v", pair)
		}
	}
	if err := checkSecretAlias("my_key", "my_key"); err != nil {
		t.Fatal("same name must allow rotation", err)
	}
	if err := checkSecretAlias("first", "second"); err != nil {
		t.Fatal(err)
	}
}
