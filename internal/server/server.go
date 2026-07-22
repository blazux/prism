package server

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"prism/internal/customtools"
	"prism/internal/docker"
	"prism/internal/mcp"
	"prism/internal/memory"
	"prism/internal/rag"

	"github.com/gorilla/websocket"
)

type Config struct {
	Port             string
	WorkspaceDir     string
	PluginDir        string
	OllamaURL        string
	Model            string
	LLMBackend       string // "ollama" (default) or "openai" (SGLang/vLLM/…)
	OpenAIBaseURL    string // /v1 root, used when LLMBackend == "openai"
	OpenAIAPIKey     string // optional bearer token for the openai backend
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
	// VoxURL, when set, means this Cortex is docked with a Vox telephony stack
	// (megazord = Vortex). It flips the UI into Vortex mode: the Téléphonie app
	// appears, and /api/vox/* proxies Vox's API (call logs, outbound calls, SIP,
	// directory). Empty = standalone Cortex, no telephony surface.
	// VoxUser/VoxPassword are Vox's HTTP Basic credentials, used by that proxy.
	VoxURL      string
	VoxUser     string
	VoxPassword string
	WebFS       embed.FS
	HelpFS      embed.FS
	ToolsFS     embed.FS
}

type Server struct {
	cfg            Config
	docker         *docker.Manager
	upgrader       websocket.Upgrader
	clients        map[*Client]struct{}
	mu             sync.RWMutex
	ragStore       *rag.Store
	ragEmbedder    *rag.Embedder
	ragCaptioner   *rag.Captioner
	ingest         *ingestTracker // live progress of synchronous RAG ingestions
	customMgr      *customtools.Manager
	memStore       *memory.Store
	mcpMgr         *mcp.Manager
	socketSessions sync.Map           // socket.io sid → targetHost (for WebSocket upgrade routing)
	channels       map[string]Channel // messaging bridges (telegram, slack, …)
	chanCancel     context.CancelFunc // cancels the running channel receive loops
	oauthStates    sync.Map           // CSRF state → oauthState (pending OAuth authorizations)
	rooms          *roomHub           // shared group chat rooms (Phase 4)
}

func New(cfg Config) *Server {
	customToolsDir := filepath.Join(cfg.WorkspaceDir, "agent_tools")
	s := &Server{
		cfg:       cfg,
		docker:    docker.NewManager(cfg.AgentContainer, cfg.WorkspaceDir, cfg.ServicePortStart, cfg.ServicePortEnd),
		clients:   make(map[*Client]struct{}),
		customMgr: customtools.NewManager(customToolsDir),
		mcpMgr:    mcp.NewManager(nil),
		rooms:     newRoomHub(),
		ingest:    newIngestTracker(),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
	s.initChannels()
	return s
}

func (s *Server) Start() error {
	// Route the standard logger through the in-memory ring (Admin → Logs) while
	// keeping stderr for docker logs. Error lines are persisted once the store
	// is up (installLogRing).
	log.SetOutput(ring)
	s.installLogRing()

	// Load or generate the AES-256 encryption key for secrets.
	encKey, err := memory.LoadOrGenerateKey(filepath.Join(s.cfg.WorkspaceDir, ".secret_key"))
	if err != nil {
		return fmt.Errorf("secret key: %w", err)
	}

	// Initialize memory store (agent config + conversation history)
	if s.cfg.PostgresURL != "" {
		go func() {
			for attempt := 1; ; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				ms, err := memory.NewStore(ctx, s.cfg.PostgresURL, encKey, s.cfg.MultiUser)
				cancel()
				if err == nil {
					s.mu.Lock()
					s.memStore = ms
					s.mu.Unlock()
					s.mcpMgr.SetStore(ms)
					log.Printf("[memory] store initialized")
					s.startChannels() // launch configured messaging bridges (telegram, slack)
					return
				}
				log.Printf("[memory] init failed (attempt %d): %v", attempt, err)
				time.Sleep(3 * time.Second)
			}
		}()
	}

	// Bundle Prism's own docs into the workspace so the agent can read them even
	// when RAG is unavailable. (RAG indexing happens in initRAG once ready.)
	helpDocs, _ := s.loadHelpDocs()
	s.materializeHelpDocs(helpDocs)

	// Seed the bundled agent tools (pcap decoder, …) into the workspace so every
	// deployment has them, and register their OS deps in .apt-packages.
	s.materializeAgentTools()

	// Initialize RAG in background (embedding probe can take a moment)
	go s.initRAG(context.Background())

	// Initialize Docker workspace in background
	go func() {
		if s.docker.IsDockerAvailable() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if err := s.docker.EnsureRunning(ctx); err != nil {
				log.Printf("[docker] workspace container unavailable: %v", err)
				log.Printf("[docker] commands will fail — start Docker and restart the server")
			}
		} else {
			log.Printf("[docker] Docker not available — exec tools will be disabled")
		}
	}()

	mux := http.NewServeMux()

	// Embedded web files
	webSub, err := fs.Sub(s.cfg.WebFS, "web")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(webSub)))

	// Dynamic plugin files
	os.MkdirAll(s.cfg.PluginDir, 0755)
	mux.Handle("/plugins/", http.StripPrefix("/plugins/", s.servePluginHTML(http.FileServer(http.Dir(s.cfg.PluginDir)))))

	// The classic "public/static folder" pattern: workspace/data/ is served verbatim
	// at /data/ — same name for the write path and the URL, nothing to translate.
	dataDir := filepath.Join(s.cfg.WorkspaceDir, "data")
	os.MkdirAll(dataDir, 0755)
	mux.Handle("/data/", http.StripPrefix("/data/", http.FileServer(http.Dir(dataDir))))

	// Browser automation screenshots — served at /screenshots/<file>
	screenshotsDir := filepath.Join(s.cfg.WorkspaceDir, ".screenshots")
	os.MkdirAll(screenshotsDir, 0755)
	mux.Handle("/screenshots/", http.StripPrefix("/screenshots/", http.FileServer(http.Dir(screenshotsDir))))

	// WebSocket
	mux.HandleFunc("/ws", s.handleWS)

	// REST API
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.HandleFunc("/api/file", s.handleFile)
	mux.HandleFunc("/api/exec", s.handleExec)
	mux.HandleFunc("/api/terminal", s.handleTerminal)
	mux.HandleFunc("/api/models", s.handleModels)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/tools", s.handleTools)
	mux.HandleFunc("/api/tool/", s.handleToolCall)
	mux.HandleFunc("/api/builtin/", s.handleBuiltinTool)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/", s.handleSessionByID)
	mux.HandleFunc("/api/chat/upload", s.handleChatFileUpload)
	mux.HandleFunc("/api/notify", s.handleExternalNotify)
	mux.HandleFunc("/api/profile", s.handleProfile)
	mux.HandleFunc("/api/avatar", s.handleAvatar)
	mux.HandleFunc("/api/voice", s.handleVoiceConfig)
	mux.HandleFunc(voxProxyPrefix, s.handleVoxProxy) // /api/vox/* → Vox's API
	mux.HandleFunc("/api/platform", s.handlePlatform)
	mux.HandleFunc("/api/admin/platform", s.handleAdminPlatform)
	mux.HandleFunc("/api/admin/usage", s.handleAdminUsage)
	mux.HandleFunc("/api/admin/logs", s.handleAdminLogs)
	mux.HandleFunc("/api/notes", s.handleNotes)
	mux.HandleFunc("/api/notes/share", s.handleNoteShare)
	mux.HandleFunc("/api/notes/source", s.handleNotesSource)
	mux.HandleFunc("/api/caldav/config", s.handleCalDAVConfig)
	mux.HandleFunc("/api/todoist/config", s.handleTodoistConfig)
	mux.HandleFunc("/api/oauth/", s.handleOAuth)
	mux.HandleFunc("/api/pim/sources", s.handlePimSources)
	mux.HandleFunc("/api/tasks", s.handleTasks)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/cron", s.handleCron)
	mux.HandleFunc("/api/personality", s.handlePersonality)
	mux.HandleFunc("/api/agent/name", s.handleAgentName)
	mux.HandleFunc("/api/agent/personality", s.handleAgentPersonality)
	mux.HandleFunc("/api/telegram/config", s.handleTelegramConfig)
	mux.HandleFunc("/api/telegram/send", s.handleTelegramSend)
	mux.HandleFunc("/api/slack/config", s.handleSlackConfig)
	mux.HandleFunc("/api/webex/config", s.handleWebexConfig)
	mux.HandleFunc("/api/webex/rooms", s.handleWebexRooms)
	mux.HandleFunc("/api/webex/send", s.handleWebexSend)
	mux.HandleFunc("/webex", s.handleWebexPage)
	mux.HandleFunc("/api/skills", s.handleSkills)
	mux.HandleFunc("/api/email/config", s.handleEmailConfig)
	mux.HandleFunc("/api/email/unread", s.handleEmailUnread)
	mux.HandleFunc("/api/email/markseen", s.handleEmailMarkSeen)
	mux.HandleFunc("/api/email/list", s.handleEmailList)
	mux.HandleFunc("/api/email/read", s.handleEmailRead)
	mux.HandleFunc("/api/email/search", s.handleEmailSearch)
	mux.HandleFunc("/api/email/send", s.handleEmailSend)
	mux.HandleFunc("/api/email/attachment", s.handleEmailAttachment)
	mux.HandleFunc("/api/email/tags", s.handleEmailTags)
	mux.HandleFunc("/api/ai/assist", s.handleAIAssist)
	mux.HandleFunc("/api/chat", s.handleChatHTTP)
	mux.HandleFunc("/api/secrets", s.handleSecrets)
	mux.HandleFunc("/api/secrets/", s.handleSecretByName)
	mux.HandleFunc("/api/user/secrets", s.handleUserSecrets)
	mux.HandleFunc("/api/user/secrets/", s.handleUserSecretByName)
	mux.HandleFunc("/api/mcp/servers", s.handleMCPServers)
	mux.HandleFunc("/api/mcp/servers/", s.handleMCPServerByID)
	mux.HandleFunc("/api/oauth/mcp/callback", s.handleMCPOAuthCallback)
	mux.HandleFunc("/api/auth", s.handleAuth)
	// Multi-user identity (Prism heavy)
	mux.HandleFunc("/api/signup", s.handleSignup)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.handleLogout)
	mux.HandleFunc("/api/me", s.handleMe)
	mux.HandleFunc("/api/admin/users", s.handleAdminUsers)
	mux.HandleFunc("/api/admin/groups", s.handleAdminGroups)
	mux.HandleFunc("/api/admin/tool-policy", s.handleAdminToolPolicy)
	mux.HandleFunc("/api/admin/group-models", s.handleAdminGroupModels)
	mux.HandleFunc("/login", s.handleLoginPage)
	mux.HandleFunc("/signup", s.handleSignupPage)
	mux.HandleFunc("/admin", s.handleAdminConsolePage)
	// Shared group chat rooms (Phase 4)
	mux.HandleFunc("/wsroom", s.handleRoomWS)
	mux.HandleFunc("/api/my/groups", s.handleMyGroups)
	mux.HandleFunc("/api/my/tool-prefs", s.handleMyToolPrefs)
	mux.HandleFunc("/api/group/members", s.handleGroupMembers)
	mux.HandleFunc("/api/room/config", s.handleRoomConfig)
	mux.HandleFunc("/api/group/tool-policy", s.handleGroupToolPolicy)
	mux.HandleFunc("/api/group/mcp", s.handleGroupMCP)
	mux.HandleFunc("/api/group/secrets", s.handleGroupSecrets)
	mux.HandleFunc("/room", s.handleRoomPage)
	mux.HandleFunc("/group", s.handleAdminConsolePage) // group admins land on the same console (role-filtered)
	mux.HandleFunc("/home", s.handleHomePage)
	s.registerRAGRoutes(mux)

	// Reverse proxy to services running inside the workspace container
	mux.HandleFunc("/proxy/", s.handleWorkspaceProxy)
	// Catch-all for absolute-path subprotocols (socket.io, etc.) emitted by
	// proxied SPAs. Routes to the correct backend using Referer/Origin.
	mux.HandleFunc("/socket.io/", s.handleSocketIOProxy)

	addr := ":" + s.cfg.Port
	log.Printf("Listening on %s", addr)
	if s.cfg.AuthToken != "" {
		log.Printf("Auth enabled (PRISM_TOKEN is set)")
	}
	return http.ListenAndServe(addr, s.withAuth(mux))
}
