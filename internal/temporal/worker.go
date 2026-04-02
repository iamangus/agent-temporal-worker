package temporal

import (
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/angoo/agent-temporal-worker/internal/config"
	"github.com/angoo/agent-temporal-worker/internal/llm"
	"github.com/angoo/agent-temporal-worker/internal/orchestrator"
)

type Worker struct {
	client     client.Client
	worker     worker.Worker
	activities *Activities
}

func NewWorker(tcfg config.TemporalConf, orchClient *orchestrator.Client, llmClient llm.Client) (*Worker, error) {
	opts := client.Options{
		HostPort:  tcfg.HostPort,
		Namespace: tcfg.Namespace,
	}
	if tcfg.APIKey != "" {
		opts.Credentials = client.NewAPIKeyStaticCredentials(tcfg.APIKey)
	}

	c, err := client.Dial(opts)
	if err != nil {
		return nil, err
	}

	acts := NewActivities(orchClient, llmClient)

	w := worker.New(c, TaskQueue, worker.Options{})
	w.RegisterWorkflow(RunAgentWorkflow)
	w.RegisterActivity(acts)

	return &Worker{
		client:     c,
		worker:     w,
		activities: acts,
	}, nil
}

func (w *Worker) Start() error {
	return w.worker.Run(worker.InterruptCh())
}

func (w *Worker) Stop() {
	w.worker.Stop()
	w.client.Close()
}
