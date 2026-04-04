package config

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type SystemConfig struct {
	Temporal     TemporalConf     `yaml:"temporal"`
	LLM          LLMConf          `yaml:"llm"`
	Orchestrator OrchestratorConf `yaml:"orchestrator"`
}

type TemporalConf struct {
	HostPort  string `yaml:"host_port"`
	Namespace string `yaml:"namespace"`
	APIKey    string `yaml:"api_key"`
}

type LLMConf struct {
	BaseURL          string            `yaml:"base_url"`
	APIKey           string            `yaml:"api_key"`
	DefaultModel     string            `yaml:"default_model"`
	Headers          map[string]string `yaml:"headers"`
	SchemaValidation bool              `yaml:"schema_validation"`
}

type OrchestratorConf struct {
	URL    string `yaml:"url"`
	APIKey string `yaml:"api_key"`
}

func DefaultSystem() *SystemConfig {
	return &SystemConfig{
		Temporal: TemporalConf{
			HostPort:  "localhost:7233",
			Namespace: "default",
		},
		LLM: LLMConf{
			BaseURL:      "https://openrouter.ai/api/v1",
			APIKey:       os.Getenv("OPENROUTER_API_KEY"),
			DefaultModel: "openai/gpt-4o",
			Headers: map[string]string{
				"HTTP-Referer": "https://github.com/angoo/agentfoundry-worker",
				"X-Title":      "agentfoundry-worker",
			},
		},
		Orchestrator: OrchestratorConf{
			URL: "http://localhost:3000",
		},
	}
}

func LoadSystem(path string) (*SystemConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := DefaultSystem()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	cfg.LLM.BaseURL = expandEnvVar(cfg.LLM.BaseURL)
	cfg.LLM.DefaultModel = expandEnvVar(cfg.LLM.DefaultModel)
	cfg.LLM.APIKey = expandEnvVar(cfg.LLM.APIKey)

	if cfg.LLM.BaseURL == "" {
		cfg.LLM.BaseURL = os.Getenv("LLM_BASE_URL")
	}
	if cfg.LLM.BaseURL == "" {
		cfg.LLM.BaseURL = "https://openrouter.ai/api/v1"
	}
	if cfg.LLM.DefaultModel == "" {
		cfg.LLM.DefaultModel = os.Getenv("LLM_DEFAULT_MODEL")
	}
	if cfg.LLM.DefaultModel == "" {
		cfg.LLM.DefaultModel = "openai/gpt-4o"
	}
	if cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = os.Getenv("LLM_API_KEY")
	}
	if cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = os.Getenv("OPENROUTER_API_KEY")
	}

	cfg.Temporal.HostPort = expandEnvVar(cfg.Temporal.HostPort)
	cfg.Temporal.Namespace = expandEnvVar(cfg.Temporal.Namespace)
	cfg.Temporal.APIKey = expandEnvVar(cfg.Temporal.APIKey)

	if cfg.Temporal.HostPort == "" {
		cfg.Temporal.HostPort = os.Getenv("TEMPORAL_HOST_PORT")
	}
	if cfg.Temporal.HostPort == "" {
		cfg.Temporal.HostPort = "localhost:7233"
	}
	if cfg.Temporal.Namespace == "" {
		cfg.Temporal.Namespace = "default"
	}
	if cfg.Temporal.APIKey == "" {
		cfg.Temporal.APIKey = os.Getenv("TEMPORAL_API_KEY")
	}

	cfg.Orchestrator.URL = expandEnvVar(cfg.Orchestrator.URL)
	cfg.Orchestrator.APIKey = expandEnvVar(cfg.Orchestrator.APIKey)

	if cfg.Orchestrator.URL == "" {
		cfg.Orchestrator.URL = os.Getenv("ORCHESTRATOR_URL")
	}
	if cfg.Orchestrator.URL == "" {
		cfg.Orchestrator.URL = "http://localhost:3000"
	}
	if cfg.Orchestrator.APIKey == "" {
		cfg.Orchestrator.APIKey = os.Getenv("ORCHESTRATOR_API_KEY")
	}

	for k, v := range cfg.LLM.Headers {
		cfg.LLM.Headers[k] = expandEnvVar(v)
	}

	return cfg, nil
}

func expandEnvVar(v string) string {
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		envVar := v[2 : len(v)-1]
		return os.Getenv(envVar)
	}
	return v
}
