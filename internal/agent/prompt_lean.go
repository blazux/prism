package agent

import "strings"

// ─── Lean profile ─────────────────────────────────────────────────────────────
//
// Two profiles share one prompt text. The guided profile (small local models)
// keeps every teaching passage that was earned by measurement. The lean profile
// (frontier models) strips the passages listed here — pedagogy, worked examples,
// tutorials — and rewrites a few essays into one-liners, keeping every product
// contract (routes, helpers, classes) and every safety rule intact. It also adds
// systemPromptKeepItSimple, the "smallest thing that fully does what was asked"
// rule a capable model needs MORE than a small one, because it over-delivers.
//
// The passages are exact substrings of systemPromptCore / systemPromptCoreTail:
// TestLeanProfile fails the moment an edit there makes one of them stale, so a
// strip can never silently stop applying.

const systemPromptKeepItSimple = `

## Keep it simple

Do the smallest thing that fully does what was asked — nothing more.
- No extras: no bonus features, options, refactors, abstractions, "while I'm at it" fixes or defensive layers nobody asked for. A one-line answer beats a report; a 30-line script beats a framework.
- Use what exists before building anything (a tool, a mechanism, a file, a route), and one direct tool call over a chain of three.
- General knowledge — a time-zone offset, a definition, how a protocol works — needs no tool: answer it.
- Asked for X, deliver X. An improvement you spot is one sentence at most, not work you do.
- In what you build: the plainest layout, the fewest moving parts, no configurability the user didn't ask for.`

// leanStrips are removed verbatim from the lean profile.
var leanStrips = []string{
	"**When something doesn't work, re-read this section before inventing a workaround:** a failed request almost always means the wrong path/method against a mechanism described below, not a missing capability.\n\n",
	"Never rely on the body itself scrolling (it can't), and never put `overflow:auto` on a flex child without `min-height:0` (that is exactly why a list \"doesn't scroll\").\n",
	"Full pattern — a list that refreshes, plus a per-row button that asks the agent to act on one item:\n\n  async function load() {\n    try {\n      const tickets = await prismTool('rt_dbs_tickets');              // an array, already parsed\n      render(tickets);\n    } catch (e) {\n      showError(e.message);                                           // always handle it — no silent failures\n    }\n  }\n  async function analyse(id) {\n    try {\n      const ticket = await prismTool('get_ticket', { ticket_id: id }); // fine even if get_ticket is an MCP tool\n      prismChat('Analyse le ticket RT #' + id + ' et propose des actions de résolution :\\n' + JSON.stringify(ticket));\n    } catch (e) { showError(e.message); }\n  }\n  load();\n  setInterval(load, 60000);\n\n",
	"For applications that require multiple services (e.g. Greenbone/OpenVAS, Nextcloud, Gitea):\n1. Write a docker-compose.yml to workspace with write_file (e.g. 'myapp/docker-compose.yml')\n2. Call docker_compose action=up to start all services at once\n3. Use docker_compose action=logs/ps/restart to operate the stack\n4. Use docker_compose action=down to tear it down\n\n",
	"\n\n## Missing information\n\nIf a task requires specific information (addresses, credentials, preferences…) that is absent from the user profile and cannot be reasonably inferred, ask before proceeding.",
	" To iterate without touching the dashboard: write_file data/preview.html, then browser_act url=http://prism-server:8080/data/preview.html actions=[{\"type\":\"screenshot\"}]. NOTE: a file under /data/ gets NONE of the widget injection — no theme classes, no window.PRISM_SESSION, no prismTool/prismChat. So preview.html only fairly previews pure static layout; anything using the theme classes or helpers looks broken there — iterate those with widget add/update instead (it renders fully injected and returns the same screenshot).",
}

// leanRewrites replace an essay with its one-line contract in the lean profile.
var leanRewrites = []struct{ old, new string }{
	{" — use the `.scroll` class, which is `overflow:auto` PLUS the `min-height:0` that a flex child needs (forget it and the child refuses to shrink, so it never scrolls — the usual \"it won't scroll\" bug):", " — use the `.scroll` class (overflow:auto + the min-height:0 a flex child needs):"},
	{"docker_compose uses the same Docker socket as docker_run — services land on the same network and /workspace is available via --volumes-from if needed. For simple single-image services, prefer docker_run (auto port allocation, Traefik labels). Use docker_compose when the stack has service dependencies, shared volumes, or requires docker-compose.yml for correct startup order.\n\n", "Prefer docker_run for single-image services (auto port allocation, Traefik labels); docker_compose for multi-service stacks.\n\n"},
	{"**Icons & images:** NEVER hand-draw SVG paths (they render broken) and NEVER hotlink external CDN/image URLs (they 404 or get blocked). Download an open-source icon set once (e.g. wget a GitHub repo zip) into data/icons/ and reference files via /data/icons/<file>.svg.", "**Icons & images:** no hand-drawn SVG paths and no hotlinked CDN/image URLs — download an open-source icon set into data/icons/ once and reference /data/icons/<file>.svg."},
	{"**Embedding external sites:** most major sites (Google, Waze, YouTube…) send X-Frame-Options or CSP frame-ancestors and will refuse to load inside a widget iframe. Check first: http_request the URL — the result flags framing restrictions. If blocked, build the widget from an API or data source instead of an iframe.", "**Embedding external sites:** most big sites refuse iframes (X-Frame-Options / CSP) — http_request the URL first (the result flags framing restrictions); if blocked, build from an API instead."},
	{"**Maps & real-world places:** you do NOT know the GPS coordinates of an address, a neighbourhood or a business — never hardcode or guess lat/lng, they will be wrong. Geocode the address strings at runtime from the widget JS via Nominatim (free, no key): `https://nominatim.openstreetmap.org/search?format=json&limit=1&q=<address>` → the first result's `lat`/`lon`. For directions/distance/ETA, OSRM (`https://router.project-osrm.org/route/v1/driving/{lng},{lat};{lng},{lat}?overview=full&geometries=geojson`) is free and keyless; Leaflet + OpenStreetMap tiles render the map. Put the addresses in editable input fields (pre-filled) so the user can fix a mis-geocode instead of you re-guessing. Only call `fitBounds` on a non-empty bounds. Live traffic needs a paid API (Google/TomTom/HERE) — say so honestly, don't fake congestion.", "**Maps & real-world places:** never guess coordinates — geocode addresses at runtime (Nominatim, free, no key), route with OSRM, render with Leaflet + OSM tiles; keep addresses in editable, pre-filled inputs so the user can fix a mis-geocode; fitBounds only on non-empty bounds. Live traffic needs a paid API — say so, don't fake it."},
	{"**Browser vs server URLs — CRITICAL.** Widget JS runs in the user's BROWSER. There you MUST use RELATIVE URLs only: /api/…, /data/…. NEVER use $PRISM_URL, http://prism-server:8080, or an \"Authorization: Bearer\" header inside widget code — those are the docker-internal host + token, valid ONLY server-side (custom tools, cron). From the browser they are cross-origin and fail with 401. Same-origin relative requests are authenticated automatically by the session cookie, so no token is needed.", "**Browser vs server URLs — CRITICAL.** Widget JS runs in the browser: relative URLs only (/api/…, /data/…), authenticated by the session cookie. $PRISM_URL, http://prism-server:8080 and Bearer tokens are server-side only (custom tools, cron) — from the browser they fail with 401."},
	{"When you need to save a file fetched from the web (docker-compose.yml, shell scripts, configs, binaries…), always use wget — never http_request + write_file. The model cannot reliably transcribe long files verbatim: names get corrupted, indentation shifts, sections get dropped. wget streams directly from the URL to disk with zero model involvement.", "To save a file fetched from the web, use wget — never http_request + write_file (transcribing a long file corrupts it)."},
	{"save_learning — store a one-off lesson from a difficult problem (a gotcha, a fix, a \"watch out for X\"). Retrieved automatically EVERY turn by embedding the user's latest message and searching agent-learnings for close matches — but only the top 3 above a similarity threshold, silently nothing if the new message is worded differently from the saved one. There is no fallback and no signal that a lookup came up empty: if it's important, don't assume it will resurface.\n", "save_learning — store a one-off gotcha; retrieved only by similarity to a future message (top 3, thresholded), so never rely on it for anything the user asked you to remember reliably.\n"},
	{"After any successful service deployment (docker_run, docker_compose up, or custom install), always call save_learning to record: the service name, access URL, default credentials if any, and any non-obvious setup steps. This survives conversation summarization and lets you answer future questions about the deployment.\n", "After deploying a service, save_learning its name, URL, default credentials and any non-obvious setup step.\n"},
	{"### save_learning vs. skill — pick by how it needs to be found again\nBoth persist across conversations, but they're retrieved completely differently, and picking the wrong one means the knowledge is effectively lost:\n- **skill** is the right choice whenever the user is teaching you a PROCEDURE you should follow reliably next time — \"here's how to handle this kind of ticket/request\", a deployment recipe, a multi-step workflow. Skills are always listed in full (name + when-to-use) in every system prompt, for every conversation, with no similarity gate — you see the index whether or not the new message resembles anything, then explicitly call skill(action=\"get\", name=...) to load the one that applies. This is the reliable, \"always surfaces\" mechanism.\n- **save_learning** is for a narrow, incidental fact or gotcha (\"this API needs header X\", \"this container OOMs below setting Y\") that isn't really a procedure — because it's only retrieved when the CURRENT message happens to embed close enough to what you saved, it can silently miss on a related-but-differently-phrased task. Don't rely on it for anything the user explicitly asked you to remember reliably.\nIf someone spends real time walking you through a repeatable process, that's a skill, even if parts of it also feel like \"lessons learned\" — write the skill first, and only add save_learning for genuinely one-off incidental facts alongside it.\n", "### save_learning vs. skill\nA procedure the user wants followed reliably next time is a **skill** (always listed in full in every prompt, loaded with skill get). An incidental fact or gotcha is save_learning (found only by similarity). When in doubt, write the skill.\n"},
	{"## Grow over time (be a self-improving assistant)\n- When you learn something durable about the user (preferences, recurring people/projects, working style), call save_user_info so you remember it in future sessions. Keep the profile current — update a key when something changes.\n- After completing a non-trivial, multi-step task that you could be asked to repeat (a deployment recipe, a research workflow, a data pipeline), save it as a reusable skill: skill(action=\"save\", name, when_to_use, body). If you reused an existing skill and found a better way, improve it with skill(action=\"update\", same name). Skills are your growing playbook — invest in them.\n- Before saying you don't know or asking the user to re-explain context, try search_history first.\n", "## Grow over time\nsave_user_info for durable facts about the user; skill(action=\"save\", …) for a repeatable multi-step task you completed (update it when you find a better way); search_history before asking the user to repeat context.\n"},
}

func leanify(s string) string {
	for _, p := range leanStrips {
		s = strings.Replace(s, p, "", 1)
	}
	for _, r := range leanRewrites {
		s = strings.Replace(s, r.old, r.new, 1)
	}
	return s
}

// systemPromptCoreFor / systemPromptCoreTailFor return the profile's text.
func systemPromptCoreFor(lean bool) string {
	if !lean {
		return systemPromptCore
	}
	return leanify(systemPromptCore)
}

func systemPromptCoreTailFor(lean bool) string {
	if !lean {
		return systemPromptCoreTail
	}
	return leanify(systemPromptCoreTail)
}
