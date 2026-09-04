package agent

import (
	"context"
	"fmt"
)

// ServerTool is a tool whose behaviour lives in package server (webhooks, PIM
// sources, channels): the definition stays in ToolDefinitions so the model
// always sees one stable schema, while the implementation is injected per
// caller through the server's CallerContext — it needs a signed-in user's
// settings store, validators (Todoist, CalDAV discovery) and the channel
// bridges, none of which belong in this package.
type ServerTool func(ctx context.Context, args map[string]any) (string, error)

func (e *ToolExecutor) SetServerTools(m map[string]ServerTool) { e.serverTools = m }

// serverTool dispatches to the injected implementation, or says plainly that
// there is none here (a group's shared agent, a voice call or a guest session
// has no personal settings to act on).
func (e *ToolExecutor) serverTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if fn, ok := e.serverTools[name]; ok && fn != nil {
		return fn(ctx, args)
	}
	return fmt.Sprintf("%s is not available in this context: it acts on a signed-in user's own settings, and this session (a shared/group agent, voice call or guest) has none. Ask the user to do it from Settings, or to ask their personal agent.", name), nil
}
