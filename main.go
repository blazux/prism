package main

import (
	"embed"
	"log"
	"os"
	"strconv"
	"strings"
	_ "time/tzdata" // embed IANA timezone database so TZ env var works without tzdata installed

	"prism/internal/server"
)

//go:embed web
var webFS embed.FS

//go:embed docs/help
var helpFS embed.FS

// all: so the sibling .apt-packages (a dotfile) rides along — go:embed skips
// dotfiles otherwise.
//
//go:embed all:agent_tools
var toolsFS embed.FS

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	workspaceDir := os.Getenv("WORKSPACE_DIR")
	if workspaceDir == "" {
		workspaceDir = "./workspace"
	}
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		log.Fatalf("Cannot create workspace: %v", err)
	}

	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}

	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = "qwen2.5-coder:7b"
	}

	// LLM backend: "ollama" (default) or "openai" for any OpenAI-compatible
	// server (SGLang, vLLM, TGI, LM Studio, OpenRouter, …).
	llmBackend := os.Getenv("LLM_BACKEND")
	if llmBackend == "" {
		llmBackend = "ollama"
	}
	openAIBaseURL := os.Getenv("OPENAI_BASE_URL")
	openAIAPIKey := os.Getenv("OPENAI_API_KEY")
	// When targeting the openai backend, OPENAI_MODEL overrides OLLAMA_MODEL so
	// the served-model-name can differ from the Ollama tag.
	if llmBackend == "openai" {
		if m := os.Getenv("OPENAI_MODEL"); m != "" {
			model = m
		}
	}

	// EMBED_BACKEND decouples RAG embeddings + vision captioning from the chat
	// backend. Empty follows LLM_BACKEND; set "ollama" to keep RAG on Ollama when
	// chat runs on a chat-only server with no /v1/embeddings (e.g. Qwen3.5+DFlash).
	// VISION_MODEL overrides the captioning model (needed when captioning runs on
	// Ollama but the chat model name isn't an Ollama tag).
	embedBackend := os.Getenv("EMBED_BACKEND")
	visionModel := os.Getenv("VISION_MODEL")

	// CHAT_VISION=false marks the chat model as text-only (e.g. MiniMax): widget
	// auto-previews are then described as text by the vision captioner instead of
	// attached as screenshots the chat model can't see.
	chatVision := os.Getenv("CHAT_VISION") != "false"

	agentContainer := os.Getenv("AGENT_CONTAINER")
	if agentContainer == "" {
		agentContainer = "prism-workspace"
	}

	pluginDir := os.Getenv("PLUGIN_DIR")
	if pluginDir == "" {
		pluginDir = "./web/plugins"
	}
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		log.Fatalf("Cannot create plugin dir: %v", err)
	}

	searxngURL := os.Getenv("SEARXNG_URL")

	postgresURL := os.Getenv("POSTGRES_URL")

	servicePortStart, _ := strconv.Atoi(os.Getenv("SERVICE_PORT_START"))
	servicePortEnd, _ := strconv.Atoi(os.Getenv("SERVICE_PORT_END"))

	embedModel := os.Getenv("EMBED_MODEL")
	if embedModel == "" {
		embedModel = "qwen3-embedding:8b"
	}

	authToken := os.Getenv("PRISM_TOKEN")

	// MULTI_USER turns on accounts, groups and rooms. Anything but "1"/"true" leaves
	// Prism single-user, which is the default and the personal case: withAuth then
	// authenticates with PRISM_TOKEN and hands every request the service identity,
	// which every scope helper reads as "global".
	multiUser := os.Getenv("MULTI_USER") == "1" || strings.EqualFold(os.Getenv("MULTI_USER"), "true")

	cfg := server.Config{
		Port:             port,
		WorkspaceDir:     workspaceDir,
		PluginDir:        pluginDir,
		OllamaURL:        ollamaURL,
		Model:            model,
		LLMBackend:       llmBackend,
		OpenAIBaseURL:    openAIBaseURL,
		OpenAIAPIKey:     openAIAPIKey,
		EmbedBackend:     embedBackend,
		VisionModel:      visionModel,
		ChatVision:       chatVision,
		AgentContainer:   agentContainer,
		SearxngURL:       searxngURL,
		PostgresURL:      postgresURL,
		EmbedModel:       embedModel,
		AuthToken:        authToken,
		MultiUser:        multiUser,
		VoxURL:           os.Getenv("VOX_URL"), // set → Vortex mode (Téléphonie app appears)
		VoxUser:          os.Getenv("VOX_USER"),
		VoxPassword:      os.Getenv("VOX_PASSWORD"),
		WebFS:            webFS,
		HelpFS:           helpFS,
		ToolsFS:          toolsFS,
		ServicePortStart: servicePortStart,
		ServicePortEnd:   servicePortEnd,
	}

	srv := server.New(cfg)
	log.Printf("Prism → http://localhost:%s", port)
	log.Printf("Workspace: %s | Ollama: %s | Model: %s", workspaceDir, ollamaURL, model)
	log.Fatal(srv.Start())
}
