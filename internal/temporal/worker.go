package temporal

import (
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/angoo/agent-temporal-worker/internal/llm"
	"github.com/angoo/agent-temporal-worker/internal/mcpclient"
	"github.com/angoo/agent-temporal-worker/internal/registry"
)

// Worker wraps the Temporal worker and its dependencies.
type Worker struct {
	client     client.Client
	worker     worker.Worker
	activities *Activities
}

// NewWorker creates a Temporal client, builds the Activities instance, and
// registers the RunAgentWorkflow and all activities on the agent-temporal-worker task queue.
func NewWorker(temporalHostPort string, reg *registry.Registry, pool *mcpclient.Pool, llmClient llm.Client) (*Worker, error) {
	c, err := client.Dial(client.Options{
		HostPort: temporalHostPort,
	})
	if err != nil {
		return nil, err
	}

	acts := NewActivities(reg, pool, llmClient)

	w := worker.New(c, TaskQueue, worker.Options{})
	w.RegisterWorkflow(RunAgentWorkflow)
	w.RegisterActivity(acts)

	return &Worker{
		client:     c,
		worker:     w,
		activities: acts,
	}, nil
}

// Start begins polling the task queue. It blocks until the worker is stopped.
func (w *Worker) Start() error {
	return w.worker.Run(worker.InterruptCh())
}

// Stop gracefully shuts down the worker and closes the Temporal client.
func (w *Worker) Stop() {
	w.worker.Stop()
	w.client.Close()
}
