package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"prism/internal/memory"
)

func TestTelephonySettingsRemainOwnerOrGlobalAdminOnly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		multi  bool
		vox    string
		role   string
		status int
	}{
		{"global admin", true, "http://vox", "global_admin", 200},
		{"group admin", true, "http://vox", "admin", 403},
		{"member", true, "http://vox", "user", 403},
		{"personal owner", false, "http://vox", "user", 200},
		{"disconnected", false, "", "user", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(Config{MultiUser: tc.multi, VoxURL: tc.vox})
			w := httptest.NewRecorder()
			s.handleTelephonySettingsPage(w, withUser(httptest.NewRequest("GET", "/apps/telephony/settings", nil), &memory.User{ID: 1, Role: tc.role}))
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if w.Code == 200 && (!strings.Contains(w.Body.String(), `id="tel-model"`) || strings.Contains(w.Body.String(), `id="adm-nav"`)) {
				t.Fatal("settings did not open as an independent app editor")
			}
		})
	}
}

func TestLegacyTelephonyLinksRetainArchiveSelection(t *testing.T) {
	s := New(Config{})
	w := httptest.NewRecorder()
	s.handleLegacyTelephonyPage(w, httptest.NewRequest("GET", "/apps/prismvox.html?group=admin&tab=logs", nil))
	if w.Code != 303 || w.Header().Get("Location") != "/apps/telephony.html?group=admin&tab=logs" {
		t.Fatalf("legacy destination: %d %s", w.Code, w.Header().Get("Location"))
	}
}
