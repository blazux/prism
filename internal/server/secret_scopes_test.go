package server

import (
	"reflect"
	"testing"
)

// groupSecretScopes is the authorization gate for reading a group's shared
// secret value. The isolation property must hold: a shared-agent session names
// a group, but that group is honored only for the service identity (a cron
// under the deployment token) — never to widen a non-member real user's reach.
func TestGroupSecretScopes(t *testing.T) {
	cases := []struct {
		name        string
		memberships []string
		session     string
		isService   bool
		want        []string
	}{
		{
			// The room-g1 morning briefing: service token, no memberships,
			// recovers its own group from the session id. This is the fix.
			"service room session recovers its group",
			nil, "room-g1", true,
			[]string{"g1"},
		},
		{
			"service webex session recovers its group",
			nil, "webex-g2-abc", true,
			[]string{"g2"},
		},
		{
			// A real member reaches their group through their membership; the
			// session-encoded group is a duplicate, not added twice.
			"member session group already covered",
			[]string{"g1"}, "room-g1", false,
			[]string{"g1"},
		},
		{
			// The isolation property: a real user who is NOT in g1 gets nothing
			// from naming room-g1 in the session.
			"non-member real user cannot widen via session",
			[]string{"g5"}, "room-g1", false,
			[]string{"g5"},
		},
		{
			"personal session, no group token",
			[]string{"g3"}, "u2-cataleya", false,
			[]string{"g3"},
		},
		{
			"service personal session, no group token",
			nil, "u2-cataleya", true,
			nil,
		},
		{
			"legacy session, service, no groups",
			nil, "default", true,
			nil,
		},
	}
	for _, c := range cases {
		got := groupSecretScopes(c.memberships, c.session, c.isService)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: groupSecretScopes(%v, %q, %v) = %v, want %v",
				c.name, c.memberships, c.session, c.isService, got, c.want)
		}
	}
}
