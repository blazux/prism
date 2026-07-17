package agent

// Permission classifies what a tool call needs the caller to be allowed to do.
// It backs the Webex channel's per-sender authorization: an incoming message is
// answered by the full agent, but each tool the agent tries to call is gated by
// the sender's granted permissions (see ToolGuard / SetToolGuard).
type Permission int

const (
	// PermRAGRead — query the knowledge base (read-only).
	PermRAGRead Permission = iota
	// PermRAGWrite — add to or delete from the knowledge base.
	PermRAGWrite
	// PermTool — any other tool call (web, docker, MCP, cron, email, …).
	PermTool
)

// Action returns a human phrase for the permission, used in refusal messages
// that the model relays to the user.
func (p Permission) Action() string {
	switch p {
	case PermRAGRead:
		return "query the knowledge base"
	case PermRAGWrite:
		return "modify the knowledge base"
	default:
		return "run tools"
	}
}

// ToolPermission maps a tool name (and its args, needed to split rag_manage's
// read vs. delete actions) to the permission it requires. Kept here next to the
// executor dispatch so the two stay in sync when tools are added.
func ToolPermission(name string, args map[string]interface{}) Permission {
	switch name {
	case "rag_search", "rag_list":
		return PermRAGRead
	case "rag_manage":
		if a, _ := args["action"].(string); a == "list" {
			return PermRAGRead
		}
		return PermRAGWrite
	case "rag_ingest", "rag_delete":
		return PermRAGWrite
	default:
		return PermTool
	}
}
