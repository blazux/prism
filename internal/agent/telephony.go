package agent

// Telephony tools (Vortex megazord). Cortex is the brain but has no phone line —
// only Vox can transfer, take a message or hang up. These tools are exposed to the
// agent ONLY on a voice call; when the agent calls one, the executor does not run
// it locally, it relays it over the WebSocket to Vox, which performs it with its
// existing ARI/SIP machinery. Schemas mirror Vox's own tool definitions so the
// arguments line up on the other side.

import "prism/internal/ollama"

// TelephonyRelay hands a telephony tool call to the voice gateway (Vox) and returns
// the short result string the model should see (so it can speak a closing line).
type TelephonyRelay func(name string, args map[string]interface{}) (string, error)

var telephonyToolNames = map[string]bool{
	"transfer_call": true,
	"take_message":  true,
	"end_call":      true,
}

// IsTelephonyTool reports whether a tool must be relayed to Vox rather than run.
func IsTelephonyTool(name string) bool { return telephonyToolNames[name] }

// PlaceCallTool lets the agent originate an outbound phone call — the agent-native
// replacement for an "outbound call" form. Unlike the relayed telephony tools it
// runs here (executor.placeCall → Vox's call queue), and it works from any channel
// (dashboard, Webex, a cron routine…). Added to the tool list only when a Vox stack
// is docked; admin-only by default (it dials real people and costs money).
var PlaceCallTool = ollama.Tool{
	Type: "function",
	Function: ollama.ToolFunction{
		Name: "place_call",
		Description: "Place an outbound phone call carried out by an AI voice agent. Use it when the user asks you to call someone (e.g. \"appelle le plombier et prends rendez-vous\"). " +
			"The call is queued and placed shortly; you are told the outcome afterwards — you do not wait on the line. Give a clear, self-contained mission: the agent on the call only knows what you write here.",
		Parameters: ollama.ToolParameters{
			Type: "object",
			Properties: map[string]ollama.ToolProperty{
				"phone_number": {Type: "string", Description: "Number to call, in international format (e.g. +596696...)."},
				"contact_name": {Type: "string", Description: "Optional name of the person/company being called, for the logs."},
				"mission":      {Type: "string", Description: "What the calling agent must accomplish and say — a clear, standalone brief (goal, key info to convey, what to obtain)."},
			},
			Required: []string{"phone_number", "mission"},
		},
	},
}

// ListCallsTool / CancelCallTool complete place_call: whatever the agent can start,
// it must be able to inspect and undo. There is deliberately no admin form for this
// (see the Telephony pane) — asking the agent *is* the interface, so the agent needs
// the whole verb set, not just the dangerous half.
var ListCallsTool = ollama.Tool{
	Type: "function",
	Function: ollama.ToolFunction{
		Name: "list_calls",
		Description: "List the outbound phone calls: those still queued, the one being dialled, and recently finished ones with their outcome. " +
			"Use it when the user asks what calls are planned or how a call went, and to find the id of a call to cancel.",
		Parameters: ollama.ToolParameters{Type: "object", Properties: map[string]ollama.ToolProperty{}},
	},
}

var CancelCallTool = ollama.Tool{
	Type: "function",
	Function: ollama.ToolFunction{
		Name: "cancel_call",
		Description: "Cancel a scheduled outbound phone call that has not been placed yet. Use it when the user changes their mind (\"finalement n'appelle pas Jean\"). " +
			"Call list_calls first to get the id. A call already being dialled cannot be cancelled.",
		Parameters: ollama.ToolParameters{
			Type: "object",
			Properties: map[string]ollama.ToolProperty{
				"call_id": {Type: "integer", Description: "Id of the queued call, as given by list_calls."},
			},
			Required: []string{"call_id"},
		},
	},
}

// OutboundCallTools are the agent-native replacement for an outbound-call form: they
// are exposed together, on any channel, whenever a Vox stack is docked.
var OutboundCallTools = []ollama.Tool{PlaceCallTool, ListCallsTool, CancelCallTool}

// TelephonyTools are added to the agent's tool list on voice calls only.
var TelephonyTools = []ollama.Tool{
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name: "transfer_call",
			Description: "Transfer the current phone call to a person — ONLY when the caller explicitly asks to be connected to someone. " +
				"Calling this tool IS the transfer: never merely say you are transferring without calling it. " +
				"Pass the contact's name (the phone system resolves who to ring); the destination is the person the caller wants to reach, never the caller. " +
				"Give the caller's name and reason only if they have actually stated them — otherwise ask for both in a SINGLE question and WAIT for the answer; do not call this tool in the same turn as that question, and never invent a name or reason.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"destination": {Type: "string", Description: "Name of the person to transfer to, e.g. Vincent"},
					"caller_name": {Type: "string", Description: "Caller's name, exactly as they said it. Omit entirely if they have not given it — never guess or use a placeholder."},
					"reason":      {Type: "string", Description: "Why the caller wants this person, in their own words. Omit entirely if not stated. \"Demande de transfert\" is not a reason."},
				},
				Required: []string{"destination"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name: "take_message",
			Description: "Record a message from the caller for a recipient. Use it when the caller asks to leave a message, or when the person they want is unavailable. Confirm the message back to the caller before calling this tool.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"recipient": {Type: "string", Description: "Name of the message recipient"},
					"content":   {Type: "string", Description: "Full content of the message"},
				},
				Required: []string{"recipient", "content"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name: "end_call",
			Description: "End the current call — ONLY when the caller has clearly signalled they are finished (an explicit goodbye, \"c'est tout merci\", \"au revoir\"). " +
				"Do NOT end after answering a single question, on a brief acknowledgement (\"ok\", \"d'accord\"), or when unsure — keep the conversation going and let the caller lead. Say a short goodbye in the same turn; the call hangs up after you speak it.",
			Parameters: ollama.ToolParameters{
				Type: "object",
			},
		},
	},
}
