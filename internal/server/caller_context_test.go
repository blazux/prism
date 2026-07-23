package server

import (
	"context"
	"testing"
)

// A service-token self-call (cron job, custom tool, widget — nil/anonymous
// user) into a shared-agent session must recover that session's GROUP scope
// from the session id itself when there's no personal scope to resolve —
// this is what makes a cron job created inside a Webex/room chat still see
// the group's RAG/MCP when it fires later. A session id that encodes nothing
// (the literal bug this guards against) must still resolve to "".
func TestCallerContextForUser_GroupSessionFallback(t *testing.T) {
	s := &Server{}
	ctx := context.Background()

	cases := []struct {
		name      string
		sessionID string
		want      string
	}{
		{"webex space session", "webex-g3-Y2lzY29zcGFyazovL3Vz", "g3"},
		{"room session", "room-g3", "g3"},
		{"bare literal (the bug)", "webex", ""},
		{"generic default board", "default", ""},
	}
	for _, c := range cases {
		cc := s.callerContextForUser(ctx, nil, c.sessionID)
		if cc.RAGScope != c.want {
			t.Errorf("%s: RAGScope=%q, want %q", c.name, cc.RAGScope, c.want)
		}
	}
}
