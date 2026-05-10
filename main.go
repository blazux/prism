package main

import (
	"embed"
	"log"
	"os"
	_ "time/tzdata" // embed IANA timezone database so TZ env var works without tzdata installed

	"prism/internal/server"
)

//go:embed web
var webFS embed.FS

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

	embedModel := os.Getenv("EMBED_MODEL")
	if embedModel == "" {
		embedModel = "qwen3-embedding:8b"
	}

	cfg := server.Config{
		Port:           port,
		WorkspaceDir:   workspaceDir,
		PluginDir:      pluginDir,
		OllamaURL:      ollamaURL,
		Model:          model,
		AgentContainer: agentContainer,
		SearxngURL:     searxngURL,
		PostgresURL:    postgresURL,
		EmbedModel:     embedModel,
		WebFS:          webFS,
	}

	srv := server.New(cfg)
	log.Printf("Prism → http://localhost:%s", port)
	log.Printf("Workspace: %s | Ollama: %s | Model: %s", workspaceDir, ollamaURL, model)
	log.Fatal(srv.Start())
}
