package registry

import (
	"log/slog"
	"sync"

	"github.com/angoo/agent-temporal-worker/internal/config"
)

// Registry stores agent definitions.
type Registry struct {
	mu        sync.RWMutex
	agentDefs map[string]*config.Definition
}

// New creates a new empty registry.
func New() *Registry {
	return &Registry{
		agentDefs: make(map[string]*config.Definition),
	}
}

// RegisterAgent stores an agent definition.
func (r *Registry) RegisterAgent(def *config.Definition) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if def.MaxTurns == 0 {
		def.MaxTurns = 10
	}

	r.agentDefs[def.Name] = def
	slog.Info("agent registered", "name", def.Name, "tools", def.Tools)
	return nil
}

// GetAgentDef returns an agent definition by name.
func (r *Registry) GetAgentDef(name string) (*config.Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.agentDefs[name]
	return def, ok
}
