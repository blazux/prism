package agent

import (
	"regexp"
	"strings"
)

// ─── Lean profile ─────────────────────────────────────────────────────────────
//
// Guided and lean retain the same operational capabilities. The guided profile (small local models)
// keeps every teaching passage that was earned by measurement. The lean profile
// (frontier models) drops the pedagogy, worked examples and tutorials, keeping
// every product contract (routes, helpers, classes) and every safety rule
// intact, and adds systemPromptTurnContract + systemPromptKeepItSimple.
//
// The lean core is a compact operational reference in prompt_lean_core.go.
// Tail differences are written INLINE in systemPromptCoreTail
// with three markers. Operational changes to the guided core also need to be
// reflected in the compact core; contract tests cover its essential interfaces:
//
//   {{guided}}passage{{/guided}}                  guided only — the lean profile drops it
//   {{guided}}essay{{lean}}one-liner{{/guided}}   each profile gets its own branch
//
// Blocks do not nest. renderProfile resolves them; TestLeanProfile fails on a
// malformed or leftover marker, on a lost product/safety passage, and when lean
// stops being meaningfully lighter than guided.

const systemPromptKeepItSimple = `

## Keep it simple

Do the smallest thing that fully does what was asked — nothing more.
- No extras: no bonus features, options, refactors, abstractions, "while I'm at it" fixes or defensive layers nobody asked for. A one-line answer beats a report; a 30-line script beats a framework.
- Use what exists before building anything (a tool, a mechanism, a file, a route), and one direct tool call over a chain of three.
- General knowledge — a time-zone offset, a definition, how a protocol works — needs no tool: answer it.
- Asked for X, deliver X. An improvement you spot is one sentence at most, not work you do.
- In what you build: the plainest layout, the fewest moving parts, no configurability the user didn't ask for.
- Keeping your memory current is part of the job, not an extra: save_user_info for a durable fact about the user, a skill for a procedure you will be asked to repeat, save_learning after a deployment.`

// systemPromptTurnContract is the one fact from systemPromptActTurn that is
// about the harness, not about the model: a reply without a tool call ends the
// turn. A frontier model does not need to be told to act, but it cannot know
// that nothing runs after its message — without this line it still ends on
// "next I'll…" and waits for a turn that never comes, and the harness nudge
// (an extra round-trip) is what saves it. Lean-only; guided has the full rule.
const systemPromptTurnContract = `

## One response is one turn

A reply with no tool call is the final answer for this turn — nothing runs afterwards and there is no later turn to continue in. If you say you are about to do something, the tool calls that do it go in that same response.`

const (
	markGuided = "{{guided}}"
	markLean   = "{{lean}}"
	markEnd    = "{{/guided}}"
)

// renderProfile resolves the profile markers in s: the guided branch of every
// block for the guided profile, the lean branch (possibly empty) for the lean
// one. A block without markEnd is left untouched, so a malformed edit is
// visible in the output — and caught by TestLeanProfile.
func renderProfile(s string, lean bool) string {
	var b strings.Builder
	for {
		i := strings.Index(s, markGuided)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := strings.Index(s[i:], markEnd)
		if end < 0 {
			b.WriteString(s)
			return b.String()
		}
		end += i
		b.WriteString(s[:i])
		block := s[i+len(markGuided) : end]
		guided, leanText := block, ""
		if k := strings.Index(block, markLean); k >= 0 {
			guided, leanText = block[:k], block[k+len(markLean):]
		}
		if lean {
			b.WriteString(leanText)
		} else {
			b.WriteString(guided)
		}
		s = s[end+len(markEnd):]
	}
}

// shoutedWords are the emphasis capitals the guided text uses to keep a small
// model's attention (do NOT, ANY tool, ALWAYS pass…). A frontier model reads
// them as noise, and a page of them buries the one section that must stay
// loud. leanTone lowercases them everywhere EXCEPT that section — Destructive
// actions — so in the lean profile capitals mean exactly one thing: data loss.
// Whole words, case-sensitive: acronyms (POST, JSON, MCP…) are not in the list.
var shoutedWords = []string{"NOT", "ANY", "NEVER", "ONLY", "ALL", "ALWAYS", "ITS", "FIRST", "AND", "CLIPPED", "SERVER-SIDE"}

const destructiveHeading = "## Destructive actions"

func leanTone(s string) string {
	if i := strings.Index(s, destructiveHeading); i >= 0 {
		rest := s[i:]
		end := len(rest)
		if j := strings.Index(rest[len(destructiveHeading):], "\n## "); j >= 0 {
			end = len(destructiveHeading) + j
		}
		return leanTone(s[:i]) + rest[:end] + leanTone(rest[end:])
	}
	return shoutedRe.ReplaceAllStringFunc(s, strings.ToLower)
}

var shoutedRe = regexp.MustCompile(`\b(` + strings.Join(shoutedWords, "|") + `)\b`)

// systemPromptCoreFor / systemPromptCoreTailFor return the profile's text.
func systemPromptCoreFor(lean bool) string {
	if lean {
		return systemPromptLeanCore
	}
	return renderProfile(systemPromptCore, false)
}

func systemPromptCoreTailFor(lean bool) string {
	if lean {
		return leanTone(renderProfile(systemPromptCoreTail, true))
	}
	return renderProfile(systemPromptCoreTail, false)
}
