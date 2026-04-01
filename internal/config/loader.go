package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// AgentRegistrar is the interface for registering agent definitions.
type AgentRegistrar interface {
	RegisterAgent(def *Definition) error
}

// Loader loads agent definitions from the filesystem and watches for changes.
type Loader struct {
	dir         string
	agentReg    AgentRegistrar
	watcher     *fsnotify.Watcher
	mu          sync.Mutex
	definitions map[string]*Definition
}

// NewLoader creates a new definition loader.
func NewLoader(dir string, agentReg AgentRegistrar) *Loader {
	return &Loader{
		dir:         dir,
		agentReg:    agentReg,
		definitions: make(map[string]*Definition),
	}
}

// LoadAll loads all YAML agent definitions from the definitions directory.
func (l *Loader) LoadAll() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := os.MkdirAll(l.dir, 0755); err != nil {
		return fmt.Errorf("create definitions dir: %w", err)
	}

	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return fmt.Errorf("read definitions dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		if err := l.loadFile(filepath.Join(l.dir, entry.Name())); err != nil {
			slog.Error("failed to load definition", "file", entry.Name(), "error", err)
			continue
		}
	}

	slog.Info("agent definitions loaded", "count", len(l.definitions))
	return nil
}

func (l *Loader) loadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read file %s: %w", path, err)
	}

	var def Definition
	if err := yaml.Unmarshal(data, &def); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	if err := def.Validate(); err != nil {
		return fmt.Errorf("validate %s: %w", path, err)
	}

	if err := l.agentReg.RegisterAgent(&def); err != nil {
		return err
	}
	l.definitions[def.Name] = &def
	slog.Info("registered agent", "name", def.Name)
	return nil
}

// Watch starts watching the definitions directory for changes.
func (l *Loader) Watch() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	l.watcher = watcher

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) {
					ext := strings.ToLower(filepath.Ext(event.Name))
					if ext == ".yaml" || ext == ".yml" {
						slog.Info("definition file changed, reloading", "file", event.Name)
						l.mu.Lock()
						if err := l.loadFile(event.Name); err != nil {
							slog.Error("failed to reload definition", "file", event.Name, "error", err)
						}
						l.mu.Unlock()
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Error("watcher error", "error", err)
			}
		}
	}()

	return watcher.Add(l.dir)
}

// Close stops the filesystem watcher.
func (l *Loader) Close() error {
	if l.watcher != nil {
		return l.watcher.Close()
	}
	return nil
}
