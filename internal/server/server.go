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
	AgentContainer   string
	SearxngURL       string
	ServicePortStart int
	ServicePortEnd   int
	PostgresURL      string
	EmbedModel       string
	AuthToken        string
	WebFS            embed.FS
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
	customMgr      *customtools.Manager
	memStore       *memory.Store
	mcpMgr         *mcp.Manager
	socketSessions sync.Map // socket.io sid → targetHost (for WebSocket upgrade routing)
}

func New(cfg Config) *Server {
	customToolsDir := filepath.Join(cfg.WorkspaceDir, "agent_tools")
	return &Server{
		cfg:       cfg,
		docker:    docker.NewManager(cfg.AgentContainer, cfg.WorkspaceDir, cfg.ServicePortStart, cfg.ServicePortEnd),
		clients:   make(map[*Client]struct{}),
		customMgr: customtools.NewManager(customToolsDir),
		mcpMgr:    mcp.NewManager(nil),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
}

func (s *Server) Start() error {
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
				ms, err := memory.NewStore(ctx, s.cfg.PostgresURL, encKey)
				cancel()
				if err == nil {
					s.mu.Lock()
					s.memStore = ms
					s.mu.Unlock()
					s.mcpMgr.SetStore(ms)
					log.Printf("[memory] store initialized")
					return
				}
				log.Printf("[memory] init failed (attempt %d): %v", attempt, err)
				time.Sleep(3 * time.Second)
			}
		}()
	}

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
	mux.Handle("/plugins/", http.StripPrefix("/plugins/", http.FileServer(http.Dir(s.cfg.PluginDir))))

	// Widget data directory — writable by cron/tools, readable by widgets via /data/<file>
	widgetDataDir := filepath.Join(s.cfg.WorkspaceDir, "widget_data")
	os.MkdirAll(widgetDataDir, 0755)
	mux.Handle("/data/", http.StripPrefix("/data/", http.FileServer(http.Dir(widgetDataDir))))

	// Browser automation screenshots — served at /screenshots/<file>
	screenshotsDir := filepath.Join(s.cfg.WorkspaceDir, ".screenshots")
	os.MkdirAll(screenshotsDir, 0755)
	mux.Handle("/screenshots/", http.StripPrefix("/screenshots/", http.FileServer(http.Dir(screenshotsDir))))

	// RAG page images — served at /rag_images/<collection>/<doc>/<page>.jpg
	ragImagesDir := filepath.Join(s.cfg.WorkspaceDir, "rag_images")
	os.MkdirAll(ragImagesDir, 0755)
	mux.Handle("/rag_images/", http.StripPrefix("/rag_images/", http.FileServer(http.Dir(ragImagesDir))))

	// WebSocket
	mux.HandleFunc("/ws", s.handleWS)

	// REST API
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.HandleFunc("/api/file", s.handleFile)
	mux.HandleFunc("/api/exec", s.handleExec)
	mux.HandleFunc("/api/models", s.handleModels)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/tools", s.handleTools)
	mux.HandleFunc("/api/tool/", s.handleToolCall)
	mux.HandleFunc("/api/builtin/", s.handleBuiltinTool)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/", s.handleSessionByID)
	mux.HandleFunc("/api/chat/upload", s.handleChatFileUpload)
	mux.HandleFunc("/api/notify", s.handleExternalNotify)
	mux.HandleFunc("/api/chat", s.handleChatHTTP)
	mux.HandleFunc("/api/secrets", s.handleSecrets)
	mux.HandleFunc("/api/secrets/", s.handleSecretByName)
	mux.HandleFunc("/api/mcp/servers", s.handleMCPServers)
	mux.HandleFunc("/api/mcp/servers/", s.handleMCPServerByID)
	mux.HandleFunc("/api/auth", s.handleAuth)
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
