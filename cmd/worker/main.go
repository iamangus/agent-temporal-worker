package main

import (
	"log/slog"
	"os"

	"github.com/angoo/agentfoundry-worker/internal/config"
	"github.com/angoo/agentfoundry-worker/internal/llm"
	"github.com/angoo/agentfoundry-worker/internal/orchestrator"
	agentworker "github.com/angoo/agentfoundry-worker/internal/temporal"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.LoadSystem("worker.yaml")
	if err != nil {
		slog.Warn("no worker.yaml found, using defaults", "error", err)
		cfg = config.DefaultSystem()
	}
	slog.Info("loaded system config",
		"temporal_host", cfg.Temporal.HostPort,
		"temporal_namespace", cfg.Temporal.Namespace,
		"orchestrator_url", cfg.Orchestrator.URL,
	)

	orchClient := orchestrator.NewClient(orchestrator.Config{
		URL:    cfg.Orchestrator.URL,
		APIKey: cfg.Orchestrator.APIKey,
	})

	llmClient := llm.NewClient(llm.ClientConfig{
		BaseURL:          cfg.LLM.BaseURL,
		APIKey:           cfg.LLM.APIKey,
		DefaultModel:     cfg.LLM.DefaultModel,
		Headers:          cfg.LLM.Headers,
		SchemaValidation: cfg.LLM.SchemaValidation,
	})

	w, err := agentworker.NewWorker(cfg.Temporal, orchClient, llmClient)
	if err != nil {
		slog.Error("failed to create Temporal worker", "error", err)
		os.Exit(1)
	}

	slog.Info("starting Temporal worker",
		"task_queue", agentworker.TaskQueue,
		"temporal", cfg.Temporal.HostPort,
		"namespace", cfg.Temporal.Namespace,
	)

	if err := w.Start(); err != nil {
		slog.Error("worker error", "error", err)
		os.Exit(1)
	}

	slog.Info("worker stopped")
}
