package main

import (
	"embed"
	"log"
	"os"
	"strconv"
	_ "time/tzdata" // embed IANA timezone database so TZ env var works without tzdata installed

	"prism/internal/server"
)

//go:embed web
var webFS embed.FS

//go:embed docs/help
var helpFS embed.FS

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

	cfg := server.Config{
		Port:             port,
		WorkspaceDir:     workspaceDir,
		PluginDir:        pluginDir,
		OllamaURL:        ollamaURL,
		Model:            model,
		LLMBackend:       llmBackend,
		OpenAIBaseURL:    openAIBaseURL,
		OpenAIAPIKey:     openAIAPIKey,
		AgentContainer:   agentContainer,
		SearxngURL:       searxngURL,
		PostgresURL:      postgresURL,
		EmbedModel:       embedModel,
		AuthToken:        authToken,
		WebFS:            webFS,
		HelpFS:           helpFS,
		ServicePortStart: servicePortStart,
		ServicePortEnd:   servicePortEnd,
	}

	srv := server.New(cfg)
	log.Printf("Prism → http://localhost:%s", port)
	log.Printf("Workspace: %s | Ollama: %s | Model: %s", workspaceDir, ollamaURL, model)
	log.Fatal(srv.Start())
}
