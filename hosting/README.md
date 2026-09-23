# Embedding Prism — experimental composition API

This package lets a separate Go module construct the same Prism product without
importing its internal packages or copying its UI, tools or help documents.

The local entry point now uses this API. Local environment variables, Docker
Compose installation and the default host Docker backend remain unchanged.

- New(Config) constructs the application and validates Docker configuration.
  It does not create deployment directories or start workers.
- Handler() assembles the product HTTP routes with Prism authentication. It
  does not initialize databases, Docker, embeddings or background work.
- Initialize(ctx) starts resources once without opening a port. Database
  readiness remains asynchronous; dependent routes return 503 until ready.
- Serve(ctx, listener) initializes, serves HTTP and drains on cancellation.
- Shutdown(ctx) rejects new work, cancels active work and waits before closing
  stores. A deadline error means shutdown is incomplete. Restart is forbidden.
- Start() retains the legacy blocking entry point. The local binary now uses
  Serve with a signal context for SIGTERM/interrupt.

Handler alone does not initialize resources. An external host can Initialize,
mount Handler, then stop accepting HTTP traffic and call Shutdown. Serve owns
its listener and handles that sequence. Neither API provides personal tenant
resolution or distributed coordination yet. One Application represents one
deployment, not one customer.

MultiUser means Prism's existing enterprise mode. It does not enable public
Cloud isolation. Do not use one Application per customer as a workaround.

The facade exposes deployment inputs, not internal stores/managers/server
pointers. Assets are embedded from their original directories once; runtime
plugins, web tests and Go source are excluded from the web asset bundle.

Pin a Prism revision. During development a separate module can use:

    require prism v0.0.0
    replace prism => /path/to/Prism

The import is prism/hosting. Release builds must resolve that replacement to
a clean checkout at a recorded commit, not to an arbitrary developer workspace.
Private adapters belong in the consuming project, never in this package.
