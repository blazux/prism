package agent

import (
	"testing"
	"unicode/utf8"
)

// The three replies qwen3.8 actually ended turns on during session model-test
// (2026-08-20) must trigger the nudge; genuine final reports must not.
func TestAnnounceTailRe(t *testing.T) {
	shouldMatch := []string{
		"OK, je continue. Je corrige l'outil pour ne garder que les vrais Free to Keep (pas les free-to-play), je remets le cron.",
		"Je passe la suite à la réalisation. D'abord je resserre l'outil sur le flag free_to_keep (les 3 vrais jeux payés→gratuits).",
		"La data est nickel — 3 vrais Free to Keep avec prix, dates, scores, images locales. Je crée le widget.",
		"Now I'll create the widget and wire the cron.",
		"The data looks good. Let me build the dashboard widget next.",
	}
	shouldNotMatch := []string{
		"C'est fait ✅ Le widget est en place, le cron tourne toutes les 6h et les 3 jeux s'affichent avec leurs images.",
		"Je viens de créer le widget : 3 jeux Free to Keep affichés, images locales, cron 6h posé.",
		"Voilà, tout est terminé. Je te laisse tester et me dire si l'affichage te va.",
		"Widget created and cron registered — everything verified with a screenshot.",
		"Salut ! Ouais, ça roule bien 🔥 Tous les systèmes en vert, le serveur tourne, rien qui fume.",
	}
	for _, s := range shouldMatch {
		if !announceTailRe.MatchString(replyTail(s)) {
			t.Errorf("expected nudge for: %q", s)
		}
	}
	for _, s := range shouldNotMatch {
		if announceTailRe.MatchString(replyTail(s)) {
			t.Errorf("false positive nudge for: %q", s)
		}
	}
}

// A long final report that *quotes* an intent phrase early on must not trigger:
// only the tail is inspected.
func TestReplyTailWindow(t *testing.T) {
	long := "Au départ je vais chercher la source, comme annoncé. " +
		"Ensuite tout a été fait : outil enregistré, data validée, images téléchargées en local, cron posé. " +
		"Résultat final : 3 jeux Free to Keep affichés dans le widget, vérifié par screenshot. Tout est en place et fonctionnel, rien d'autre à faire."
	if announceTailRe.MatchString(replyTail(long)) {
		t.Errorf("intent phrase outside the 200-rune tail should not trigger")
	}
}

func TestSanitizeForDB(t *testing.T) {
	gzipish := "header \x1f\x8b\x08 body \x00 tail"
	got := sanitizeForDB(gzipish)
	for i, r := range got {
		if r == 0 {
			t.Errorf("NUL byte survived at %d", i)
		}
	}
	if !utf8.ValidString(got) {
		t.Errorf("invalid UTF-8 survived: %q", got)
	}
	clean := "réponse normale ✅"
	if sanitizeForDB(clean) != clean {
		t.Errorf("clean string was altered")
	}
}
