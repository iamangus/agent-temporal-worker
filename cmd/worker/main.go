package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/angoo/agent-temporal-worker/internal/config"
	"github.com/angoo/agent-temporal-worker/internal/llm"
	"github.com/angoo/agent-temporal-worker/internal/mcpclient"
	"github.com/angoo/agent-temporal-worker/internal/registry"
	agentworker "github.com/angoo/agent-temporal-worker/internal/temporal"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// Load system config.
	cfg, err := config.LoadSystem("worker.yaml")
	if err != nil {
		slog.Warn("no worker.yaml found, using defaults", "error", err)
		cfg = config.DefaultSystem()
	}
	slog.Info("loaded system config",
		"definitions_dir", cfg.DefinitionsDir,
		"temporal_host", cfg.Temporal.HostPort,
		"temporal_namespace", cfg.Temporal.Namespace,
		"mcp_servers", len(cfg.MCPServers),
	)

	// Create registry (stores agent definitions).
	reg := registry.New()

	// Create MCP client pool and connect to configured external MCP servers.
	pool := mcpclient.NewPool()
	ctx := context.Background()
	if len(cfg.MCPServers) > 0 {
		if err := pool.Connect(ctx, cfg.MCPServers); err != nil {
			slog.Error("failed to connect to MCP servers", "error", err)
		}
	} else {
		slog.Info("no external MCP servers configured")
	}

	// Create LLM client.
	llmClient := llm.NewClient(llm.ClientConfig{
		BaseURL:          cfg.LLM.BaseURL,
		APIKey:           cfg.LLM.APIKey,
		DefaultModel:     cfg.LLM.DefaultModel,
		Headers:          cfg.LLM.Headers,
		SchemaValidation: cfg.LLM.SchemaValidation,
	})

	// Load agent definitions from the filesystem.
	loader := config.NewLoader(cfg.DefinitionsDir, reg)
	if err := loader.LoadAll(); err != nil {
		slog.Error("failed to load definitions", "error", err)
		os.Exit(1)
	}

	// Start hot-reload watcher so agent definitions update without a restart.
	if err := loader.Watch(); err != nil {
		slog.Warn("failed to start filesystem watcher", "error", err)
	}
	defer loader.Close()

	// Create and start the Temporal worker.
	w, err := agentworker.NewWorker(cfg.Temporal, reg, pool, llmClient)
	if err != nil {
		slog.Error("failed to create Temporal worker", "error", err)
		pool.Close()
		os.Exit(1)
	}
	defer pool.Close()

	slog.Info("starting Temporal worker",
		"task_queue", agentworker.TaskQueue,
		"temporal", cfg.Temporal.HostPort,
		"namespace", cfg.Temporal.Namespace,
	)

	// Start blocks until interrupted (SIGINT/SIGTERM via worker.InterruptCh).
	if err := w.Start(); err != nil {
		slog.Error("worker error", "error", err)
		os.Exit(1)
	}

	slog.Info("worker stopped")
}
