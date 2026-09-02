package server

// One-shot LLM assist endpoint shared by the Email app (summarize / draft reply
// / triage) and the Documents app (rewrite / continue / fix grammar / custom).
// Unlike the chat agent, this runs a single completion with no tools or history.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"prism/internal/ollama"
)

var thinkRE = regexp.MustCompile(`(?is)<think>.*?</think>`)

func stripThink(s string) string {
	return strings.TrimSpace(thinkRE.ReplaceAllString(s, ""))
}

// assistPrompts maps a task to a system instruction. The user message is the
// caller's text, optionally prefixed with a free-form instruction.
var assistPrompts = map[string]string{
	"summarize":      "Summarize the following text concisely. Output only the summary, no preamble.",
	"email_summary":  "Summarize this email in 2-4 short bullet points: who sent it, what they want, and any deadline or action needed. Be terse. Output only the bullets.",
	"email_reply":    "Write a clear, polite, concise reply to the email below, from the recipient's point of view. Match a professional but friendly tone unless told otherwise. Output ONLY the reply body — no subject line, no greeting placeholders like [Name], no commentary.",
	"email_triage":   "You are an email triage assistant. The input is a JSON array of emails (index, from, subject, snippet). Classify each one. Reply with ONLY a JSON array of objects {\"i\": <index>, \"category\": <one of: Action, FYI, Newsletter, Personal, Promo, Spam>, \"tags\": [up to 2 short lowercase tags]}. No prose, no code fences.",
	"doc_rewrite":    "Rewrite the text below to improve clarity and flow while preserving meaning. Output ONLY the rewritten text, no commentary.",
	"doc_continue":   "Continue writing naturally from where the text ends, in the same voice and style. Output ONLY the continuation (do not repeat the existing text).",
	"doc_grammar":    "Fix spelling, grammar and punctuation in the text below. Preserve the meaning, tone and Markdown formatting. Output ONLY the corrected text.",
	"doc_shorten":    "Make the text below more concise without losing key information. Output ONLY the shortened text.",
	"doc_summarize":  "Summarize the text below into a short paragraph. Output only the summary.",
	"task_breakdown": "Break the user's objective into a short ordered list of concrete, actionable sub-tasks (aim for 3-8). Each should start with a verb and be self-contained. Reply with ONLY a JSON array of short task-title strings. No prose, no numbering, no code fences.",
}

// POST /api/ai/assist  {task, text, instruction?}  ->  {result}
func (s *Server) handleAIAssist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var b struct {
		Task        string `json:"task"`
		Text        string `json:"text"`
		Instruction string `json:"instruction"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil {
		http.Error(w, "bad body", 400)
		return
	}
	if strings.TrimSpace(b.Text) == "" && strings.TrimSpace(b.Instruction) == "" {
		http.Error(w, "text or instruction required", 400)
		return
	}

	system := assistPrompts[b.Task]
	if system == "" {
		// Unknown task → treat instruction as the directive (custom action).
		system = "Follow the user's instruction on the provided text. Output only the result, no preamble."
	}
	user := b.Text
	if b.Instruction != "" {
		user = "Instruction: " + b.Instruction + "\n\n---\n" + b.Text
	}

	start := time.Now()
	log.Printf("[ai/assist] task=%q model=%q textlen=%d", b.Task, s.cfg.Model, len(b.Text))

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	ch := make(chan ollama.StreamEvent, 200)
	req := ollama.ChatRequest{
		Model: s.cfg.Model,
		Messages: []ollama.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		// Assists are one-shot utilities ("output only the result"). On a
		// reasoning model, without this the whole 120s budget can go to the
		// thinking channel and the handler returns {"result":""} — measured on
		// email_triage: "done in 2m0s, 0 chars", a silent no-op in the UI.
		NoThinking: true,
	}
	backend := s.newChatBackend()
	go func() {
		backend.Chat(ctx, req, ch)
		close(ch)
	}()

	var out strings.Builder
	for ev := range ch {
		if ev.Err != nil {
			log.Printf("[ai/assist] task=%q LLM error after %s: %v", b.Task, time.Since(start).Round(time.Millisecond), ev.Err)
			http.Error(w, ev.Err.Error(), 502)
			return
		}
		out.WriteString(ev.Content)
	}
	result := stripThink(out.String())
	log.Printf("[ai/assist] task=%q done in %s, %d chars", b.Task, time.Since(start).Round(time.Millisecond), out.Len())
	// Empty content is a failure (timeout mid-reasoning, model burned its budget
	// thinking…) — say so instead of handing the UI an empty string to choke on.
	if result == "" {
		http.Error(w, "the model returned no content (it may have spent the whole budget reasoning)", 502)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"result": result})
}
