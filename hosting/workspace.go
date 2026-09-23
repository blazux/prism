package hosting

import (
	"context"
	"net/http"

	"prism/internal/docker"
	"prism/internal/server"
)

// WorkspaceAccess is a trusted, owner-bound capability. Root is an operator
// supplied POSIX directory; Execute must enforce the assignment at execution
// time. Lifetime is cancelled when the account or its assignment is revoked.
// None of these values may be taken from client parameters.
type WorkspaceAccess struct {
	Data     *PersonalData
	Owner    string
	Root     string
	Lifetime context.Context
	Execute  func(context.Context, string, []byte, map[string]string) (string, error)
}

// PersonalWorkspaceHandler exposes only files and command execution. It is an
// experimental integration slice, NOT the complete hosted Prism application.
// The caller must authenticate each request in resolve, enforce Host/Origin and
// bound request sizes. Unknown routes never fall back to standalone Prism.
func PersonalWorkspaceHandler(resolve func(*http.Request) (WorkspaceAccess, error)) http.Handler {
	if resolve == nil {
		return server.PersonalWorkspaceHandler(nil)
	}
	return server.PersonalWorkspaceHandler(func(r *http.Request) (server.WorkspaceAccess, error) {
		a, err := resolve(r)
		var data *server.PersonalData
		if a.Data != nil {
			data = a.Data.data
		}
		return server.WorkspaceAccess{Data: data, Owner: a.Owner, Root: a.Root, Lifetime: a.Lifetime, Execute: docker.WorkspaceExecution(a.Execute)}, err
	})
}
