package server

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"prism/internal/customtools"
	"prism/internal/docker"
	"prism/internal/mcp"
	"prism/internal/memory"
	"prism/internal/rag"

	"github.com/gorilla/websocket"
)

type Config struct {
	WebhookURL                     func(string, string) string
	HostedPersonal                 bool
	WebhookConcurrency             int
	ServiceURL                     func(int) string
	WorkspaceDial                  func(context.Context, int) (net.Conn, error)
	DisableAIServerDefaults        bool   // hosting mode without usable server AI defaults; local default remains enabled
	CapabilityPrefix               string // opaque routing envelope supplied by a trusted embedding host
	WidgetFrameURL, WidgetGrantURL string // optional isolated widget host supplied by embedding application
	DockerBackend                  docker.Backend
	AISources                      []aiSource
	AIDefaultSource                string
	Port                           string
	WorkspaceDir                   string
	SecretKeyPath                  string
	PluginDir                      string
	OllamaURL                      string
	Model                          string
	LLMBackend                     string // "ollama" (default), "openai" (SGLang/vLLM/…) or "anthropic" (Claude, API key)
	OpenAIBaseURL                  string // /v1 root, used when LLMBackend == "openai"
	OpenAIAPIKey                   string // optional bearer token for the openai backend
	// AnthropicToken is the console API key for the Claude backend. A Pro/Max
	// subscription token is not an option — see internal/anthropic/credential.go.
	AnthropicToken   string
	AnthropicBaseURL string // API root; empty = https://api.anthropic.com
	// AnthropicModel is the Claude model to use, held separately from Model so
	// Claude can sit in the picker alongside a local default rather than only as
	// the primary backend.
	AnthropicModel   string
	EmbedBackend     string // "" (follow LLMBackend), "ollama" or "openai": backend for RAG embeddings + captioning
	VisionModel      string // optional override model for RAG vision captioning (needed when captioning ≠ chat backend)
	ChatVision       bool   // chat model can see images (default true); false → caption widget previews as text for a text-only model
	AgentContainer   string
	SearxngURL       string
	ServicePortStart int
	ServicePortEnd   int
	PostgresURL      string
	EmbedModel       string
	AuthToken        string
	// MultiUser turns Prism from a personal dashboard into a shared one: accounts,
	// groups, rooms, per-user scoping and a login page. Off by default — at home
	// there is one user, and AuthToken is the whole of authentication. It is read
	// in exactly one place, withAuth; everything downstream reads the identity that
	// puts in the request, and the service identity already means "global".
	MultiUser bool
	// VoxURL, when set, means this Prism is docked with a Prism Vox telephony
	// stack: the Téléphonie app appears, and /api/vox/* proxies Vox's API (call
	// logs, outbound calls, SIP, directory). Empty = standalone Prism, no
	// telephony surface.
	// VoxUser/VoxPassword are Vox's HTTP Basic credentials, used by that proxy.
	VoxURL      string
	VoxUser     string
	VoxPassword string
	WebFS       fs.FS
	HelpFS      fs.FS
	ToolsFS     fs.FS
}

type Server struct {
	completedRuns map[string]*chatRun

	runsMu sync.Mutex
	runs   map[string]*chatRun

	webhookActive   atomic.Int32
	hostedResolver  func(*http.Request) (*Server, error)
	life            lifecycle
	authMu          sync.Mutex
	authWindows     map[string]authWindow
	cfg             Config
	docker          *docker.Manager
	upgrader        websocket.Upgrader
	clients         map[*Client]struct{}
	mu              sync.RWMutex
	ragMu           sync.RWMutex
	ragApplyMu      sync.Mutex
	ragInitStatus   atomic.Value // resource-local embedding status, never process-global
	ragUpdating     atomic.Bool
	ragGeneration   uint64             // protected by mu
	ragCancel       context.CancelFunc // protected by mu
	activeEmbedding *aiProfile         // currently active embedding configuration; protected by mu
	ragStore        *rag.Store
	ragEmbedder     *rag.Embedder
	ragCaptioner    *rag.Captioner
	ingest          *ingestTracker // live progress of synchronous RAG ingestions
	customMgr       *customtools.Manager
	memStore        *memory.Store
	mcpMgr          *mcp.Manager
	socketSessions  sync.Map           // socket.io sid → targetHost (for WebSocket upgrade routing)
	channels        map[string]Channel // messaging bridges (telegram, slack, …)
	chanCancel      context.CancelFunc // cancels the running channel receive loops
	oauthStates     sync.Map           // CSRF state → oauthState (pending OAuth authorizations)
	rooms           *roomHub           // shared group chat rooms (Phase 4)
	toolLimiter     *toolLimiter       // caps concurrent tool execs so a runaway widget can't OOM the host
}

func New(cfg Config) *Server {
	customToolsDir := filepath.Join(cfg.WorkspaceDir, "agent_tools")
	s := &Server{
		cfg:         cfg,
		docker:      docker.NewManager(cfg.AgentContainer, cfg.WorkspaceDir, cfg.ServicePortStart, cfg.ServicePortEnd, cfg.DockerBackend),
		clients:     make(map[*Client]struct{}),
		customMgr:   customtools.LoadManager(customToolsDir),
		mcpMgr:      mcp.NewManager(nil),
		rooms:       newRoomHub(),
		ingest:      newIngestTracker(),
		toolLimiter: newToolLimiter(),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// Default websocket origin check permits only the request host.
		},
	}
	s.initChannels()
	return s
}

func (s *Server) initialize(runtime context.Context) error {
	if err := s.cfg.DockerBackend.Validate(); err != nil {
		return err
	}
	if s.docker.WorkspaceDocker() && !s.docker.IsDockerAvailable() {
		return fmt.Errorf("workspace Docker unavailable: provision the internal daemon and check WORKSPACE_DOCKER_SOCKET and WORKSPACE_DOCKER_USER; no host fallback")
	}
	// Route the standard logger through the in-memory ring (Admin → Logs) while
	// keeping stderr for docker logs. Error lines are persisted once the store
	// is up (installLogRing).
	log.SetOutput(ring)
	s.installLogRing()

	// Load or generate the AES-256 encryption key for secrets.
	keyPath := s.cfg.SecretKeyPath
	if keyPath == "" {
		keyPath = filepath.Join(filepath.Dir(s.cfg.WorkspaceDir), ".prism-private", filepath.Base(s.cfg.WorkspaceDir)+".key")
	}
	// Resolve an existing ancestor too, so an operator cannot accidentally place
	// the private store back inside the shared execution volume through a link.
	if withinResolved(s.cfg.WorkspaceDir, keyPath) {
		return fmt.Errorf("SECRET_KEY_PATH must be outside WORKSPACE_DIR")
	}
	encKey, err := memory.LoadPrivateKey(keyPath, filepath.Join(s.cfg.WorkspaceDir, ".secret_key"))
	if err != nil {
		return fmt.Errorf("secret key: %w", err)
	}

	// Initialize memory store (agent config + conversation history)
	if s.cfg.PostgresURL != "" {
		s.background(func() {
			for attempt := 1; ; attempt++ {
				ctx, cancel := context.WithTimeout(runtime, 15*time.Second)
				ms, err := memory.NewStore(ctx, s.cfg.PostgresURL, encKey, s.cfg.MultiUser)
				cancel()
				if err == nil {
					s.mu.Lock()
					s.memStore = ms
					s.mu.Unlock()
					s.mcpMgr.SetStore(ms)
					log.Printf("[memory] store initialized")
					s.startEmailRules()
					s.startChannels() // launch configured messaging bridges (telegram, slack)
					return
				}
				log.Printf("[memory] init failed (attempt %d): %v", attempt, err)
				select {
				case <-runtime.Done():
					return
				case <-time.After(3 * time.Second):
				}
			}
		})
	}

	// Bundle Prism's own docs into the workspace so the agent can read them even
	// when RAG is unavailable. (RAG indexing happens in initRAG once ready.)
	helpDocs, _ := s.loadHelpDocs()
	s.materializeHelpDocs(helpDocs)

	// Seed the bundled agent tools (pcap decoder, …) into the workspace so every
	// deployment has them, and register their OS deps in .apt-packages.
	s.materializeAgentTools()

	// Initialize RAG in background (embedding probe can take a moment)
	s.background(func() { s.initRAG(runtime) })

	// Initialize Docker workspace in background
	s.background(func() {
		if s.docker.IsDockerAvailable() {
			ctx, cancel := context.WithTimeout(runtime, 10*time.Minute)
			defer cancel()
			if err := s.docker.EnsureRunning(ctx); err != nil {
				log.Printf("[docker] workspace container unavailable: %v", err)
				log.Printf("[docker] commands will fail — start Docker and restart the server")
			}
		} else {
			log.Printf("[docker] Docker not available — exec tools will be disabled")
		}
	})

	for _, dir := range []string{s.cfg.PluginDir, filepath.Join(s.cfg.WorkspaceDir, "data"), filepath.Join(s.cfg.WorkspaceDir, ".screenshots")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}

// Start preserves the standalone entry point; hosted callers supply a context.
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", ":"+s.cfg.Port)
	if err != nil {
		return err
	}
	return s.Serve(context.Background(), listener)
}

// Handler assembles routes without initializing resources or background work.
func (s *Server) Handler() (http.Handler, error) {
	mux := http.NewServeMux()

	// Embedded web files
	webSub, err := fs.Sub(s.cfg.WebFS, "web")
	if err != nil {
		return nil, err
	}
	mux.Handle("/", http.FileServer(http.FS(webSub)))

	// Generated content must use the same owner resolution as tools and data.
	mux.HandleFunc("/plugins/", s.resourceRoute(func(s *Server, w http.ResponseWriter, r *http.Request) {
		http.StripPrefix("/plugins/", s.servePluginHTML(http.FileServer(s.generatedFS(s.cfg.PluginDir)))).ServeHTTP(w, r)
	}))
	mux.HandleFunc("/data/", s.resourceRoute(func(s *Server, w http.ResponseWriter, r *http.Request) {
		http.StripPrefix("/data/", http.FileServer(s.generatedFS(filepath.Join(s.cfg.WorkspaceDir, "data")))).ServeHTTP(w, r)
	}))
	mux.HandleFunc("/screenshots/", s.resourceRoute(func(s *Server, w http.ResponseWriter, r *http.Request) {
		http.StripPrefix("/screenshots/", http.FileServer(s.generatedFS(filepath.Join(s.cfg.WorkspaceDir, ".screenshots")))).ServeHTTP(w, r)
	}))

	// WebSocket
	mux.HandleFunc("/ws", s.resourceRoute((*Server).handleWS))

	// REST API
	mux.HandleFunc("/api/files", s.resourceRoute((*Server).handleFiles))
	mux.HandleFunc("/api/file", s.resourceRoute((*Server).handleFile))
	mux.HandleFunc("/api/exec", s.resourceRoute((*Server).handleExec))
	mux.HandleFunc("/api/terminal", s.resourceRoute((*Server).handleTerminal))
	mux.HandleFunc("/api/models", s.resourceRoute((*Server).handleModels))
	mux.HandleFunc("/api/ai/config", s.resourceRoute((*Server).handleAIProfile))
	mux.HandleFunc("/api/status", s.resourceRoute((*Server).handleStatus))
	mux.HandleFunc("/api/tools", s.resourceRoute((*Server).handleTools))
	mux.HandleFunc("/api/tool/", s.resourceRoute((*Server).handleToolCall))
	mux.HandleFunc("/api/builtin/", s.resourceRoute((*Server).handleBuiltinTool))
	mux.HandleFunc("/api/sessions", s.resourceRoute((*Server).handleSessions))
	mux.HandleFunc("/api/sessions/", s.resourceRoute((*Server).handleSessionByID))
	mux.HandleFunc("/api/chat/upload", s.resourceRoute((*Server).handleChatFileUpload))
	mux.HandleFunc("/api/notify", s.resourceRoute((*Server).handleExternalNotify))
	mux.HandleFunc("/api/profile", s.resourceRoute((*Server).handleProfile))
	mux.HandleFunc("/api/avatar", s.resourceRoute((*Server).handleAvatar))
	mux.HandleFunc("/api/voice", s.resourceRoute((*Server).handleVoiceConfig))
	mux.HandleFunc("/api/voice/caller", s.resourceRoute((*Server).handleVoiceCaller))       // Vox: who is calling?
	mux.HandleFunc("/api/voice/directory", s.resourceRoute((*Server).handleVoiceDirectory)) // Vox: who can I transfer to?
	mux.HandleFunc("/api/voice/search", s.resourceRoute((*Server).handleVoiceSearch))       // Vox: what do we know?
	mux.HandleFunc("/api/voice/kb", s.resourceRoute((*Server).handleVoiceKB))               // which collection the switchboard reads
	mux.HandleFunc(voxProxyPrefix, s.resourceRoute((*Server).handleVoxProxy))               // /api/vox/* → Vox's API
	mux.HandleFunc("/api/platform", s.resourceRoute((*Server).handlePlatform))
	mux.HandleFunc("/api/admin/platform", s.resourceRoute((*Server).handleAdminPlatform))
	mux.HandleFunc("/api/admin/usage", s.resourceRoute((*Server).handleAdminUsage))
	mux.HandleFunc("/api/activity", s.resourceRoute((*Server).handleActivity))
	mux.HandleFunc("/api/shared", s.resourceRoute((*Server).handleShared))
	mux.HandleFunc("/api/shared/", s.resourceRoute((*Server).handleSharedItem))
	mux.HandleFunc("/api/admin/logs", s.resourceRoute((*Server).handleAdminLogs))
	mux.HandleFunc("/api/notes", s.resourceRoute((*Server).handleNotes))
	mux.HandleFunc("/api/notes/share", s.resourceRoute((*Server).handleNoteShare))
	mux.HandleFunc("/api/notes/source", s.resourceRoute((*Server).handleNotesSource))
	mux.HandleFunc("/api/notes/image", s.resourceRoute((*Server).handleNoteImage))
	mux.HandleFunc("/api/caldav/config", s.resourceRoute((*Server).handleCalDAVConfig))
	mux.HandleFunc("/api/todoist/config", s.resourceRoute((*Server).handleTodoistConfig))
	mux.HandleFunc("/api/oauth/", s.resourceRoute((*Server).handleOAuth))
	mux.HandleFunc("/api/pim/sources", s.resourceRoute((*Server).handlePimSources))
	mux.HandleFunc("/api/tasks", s.resourceRoute((*Server).handleTasks))
	mux.HandleFunc("/api/events", s.resourceRoute((*Server).handleEvents))
	mux.HandleFunc("/api/cron", s.resourceRoute((*Server).handleCron))
	mux.HandleFunc("/api/webhooks", s.resourceRoute((*Server).handleWebhooks))
	mux.HandleFunc("/api/webhooks/", s.resourceRoute((*Server).handleWebhookByID))
	// Inbound webhook calls come from machines with no Prism login; they are
	// authenticated by the per-webhook token instead (see webhookPublicPath).
	mux.HandleFunc("/api/webhook/", s.resourceRoute((*Server).handleWebhookIncoming))
	mux.HandleFunc("/api/personality", s.resourceRoute((*Server).handlePersonality))
	mux.HandleFunc("/api/agent/name", s.resourceRoute((*Server).handleAgentName))
	mux.HandleFunc("/api/agent/limits", s.resourceRoute((*Server).handleAgentLimits))
	mux.HandleFunc("/api/agent/personality", s.resourceRoute((*Server).handleAgentPersonality))
	mux.HandleFunc("/api/telegram/config", s.resourceRoute((*Server).handleTelegramConfig))
	mux.HandleFunc("/api/telegram/send", s.resourceRoute((*Server).handleTelegramSend))
	mux.HandleFunc("/api/slack/config", s.resourceRoute((*Server).handleSlackConfig))
	mux.HandleFunc("/api/webex/config", s.resourceRoute((*Server).handleWebexConfig))
	mux.HandleFunc("/api/webex/rooms", s.resourceRoute((*Server).handleWebexRooms))
	mux.HandleFunc("/api/webex/send", s.resourceRoute((*Server).handleWebexSend))
	mux.HandleFunc("/api/skills", s.resourceRoute((*Server).handleSkills))
	mux.HandleFunc("/api/email/config", s.resourceRoute((*Server).handleEmailConfig))
	mux.HandleFunc("/api/email/unread", s.resourceRoute((*Server).handleEmailUnread))
	mux.HandleFunc("/api/email/markseen", s.resourceRoute((*Server).handleEmailMarkSeen))
	mux.HandleFunc("/api/email/list", s.resourceRoute((*Server).handleEmailList))
	mux.HandleFunc("/api/email/read", s.resourceRoute((*Server).handleEmailRead))
	mux.HandleFunc("/api/email/search", s.resourceRoute((*Server).handleEmailSearch))
	mux.HandleFunc("/api/email/send", s.resourceRoute((*Server).handleEmailSend))
	mux.HandleFunc("/api/email/attachment", s.resourceRoute((*Server).handleEmailAttachment))
	mux.HandleFunc("/api/email/tags", s.resourceRoute((*Server).handleEmailTags))
	mux.HandleFunc("/api/ai/assist", s.resourceRoute((*Server).handleAIAssist))
	mux.HandleFunc("/api/chat", s.resourceRoute((*Server).handleChatHTTP))
	mux.HandleFunc("/api/secrets", s.resourceRoute((*Server).handleSecrets))
	mux.HandleFunc("/api/secrets/", s.resourceRoute((*Server).handleSecretByName))
	mux.HandleFunc("/api/user/secrets", s.resourceRoute((*Server).handleUserSecrets))
	mux.HandleFunc("/api/user/secrets/", s.resourceRoute((*Server).handleUserSecretByName))
	mux.HandleFunc("/api/mcp/servers", s.resourceRoute((*Server).handleMCPServers))
	mux.HandleFunc("/api/mcp/servers/", s.resourceRoute((*Server).handleMCPServerByID))
	mux.HandleFunc("/api/oauth/mcp/callback", s.resourceRoute((*Server).handleMCPOAuthCallback))
	mux.HandleFunc("/api/auth", s.resourceRoute((*Server).handleAuth))
	// Multi-user identity (Prism heavy)
	mux.HandleFunc("/api/signup", s.resourceRoute((*Server).handleSignup))
	mux.HandleFunc("/api/login", s.resourceRoute((*Server).handleLogin))
	mux.HandleFunc("/api/logout", s.resourceRoute((*Server).handleLogout))
	mux.HandleFunc("/api/me", s.resourceRoute((*Server).handleMe))
	mux.HandleFunc("/api/admin/users", s.resourceRoute((*Server).handleAdminUsers))
	mux.HandleFunc("/api/admin/groups", s.resourceRoute((*Server).handleAdminGroups))
	mux.HandleFunc("/api/admin/tool-policy", s.resourceRoute((*Server).handleAdminToolPolicy))
	mux.HandleFunc("/api/admin/group-models", s.resourceRoute((*Server).handleAdminGroupModels))
	mux.HandleFunc("/login", s.resourceRoute((*Server).handleLoginPage))
	mux.HandleFunc("/signup", s.resourceRoute((*Server).handleSignupPage))
	mux.HandleFunc("/admin", s.resourceRoute((*Server).handleAdminConsolePage))
	// Shared group chat rooms (Phase 4)
	mux.HandleFunc("/wsroom", s.resourceRoute((*Server).handleRoomWS))
	mux.HandleFunc("/api/my/groups", s.resourceRoute((*Server).handleMyGroups))
	mux.HandleFunc("/api/my/tool-prefs", s.resourceRoute((*Server).handleMyToolPrefs))
	mux.HandleFunc("/api/group/members", s.resourceRoute((*Server).handleGroupMembers))
	mux.HandleFunc("/api/room/config", s.resourceRoute((*Server).handleRoomConfig))
	mux.HandleFunc("/api/group/tool-policy", s.resourceRoute((*Server).handleGroupToolPolicy))
	mux.HandleFunc("/api/group/mcp", s.resourceRoute((*Server).handleGroupMCP))
	mux.HandleFunc("/api/group/secrets", s.resourceRoute((*Server).handleGroupSecrets))
	mux.HandleFunc("/room", s.resourceRoute((*Server).handleRoomPage))
	mux.HandleFunc("/group", s.resourceRoute((*Server).handleAdminConsolePage)) // group admins land on the same console (role-filtered)
	mux.HandleFunc("/home", s.resourceRoute((*Server).handleHomePage))
	s.registerRAGRoutes(mux)

	// Reverse proxy to services running inside the workspace container
	mux.HandleFunc("/proxy/", s.resourceRoute((*Server).handleWorkspaceProxy))
	// Catch-all for absolute-path subprotocols (socket.io, etc.) emitted by
	// proxied SPAs. Routes to the correct backend using Referer/Origin.
	mux.HandleFunc("/socket.io/", s.resourceRoute((*Server).handleSocketIOProxy))

	return s.lifecycleHandler(s.withAuth(mux)), nil
}
