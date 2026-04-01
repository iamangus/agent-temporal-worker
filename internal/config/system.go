package config

import (
	"os"
	"strings"

	"github.com/angoo/agent-temporal-worker/internal/mcpclient"
	"gopkg.in/yaml.v3"
)

// SystemConfig is the top-level configuration for the agent-temporal-worker daemon.
type SystemConfig struct {
	DefinitionsDir string                   `yaml:"definitions_dir"`
	LLM            LLMConf                  `yaml:"llm"`
	MCPServers     []mcpclient.ServerConfig `yaml:"mcp_servers"`
}

// LLMConf configures the OpenAI-compatible LLM provider.
type LLMConf struct {
	BaseURL          string            `yaml:"base_url"`
	APIKey           string            `yaml:"api_key"`
	DefaultModel     string            `yaml:"default_model"`
	Headers          map[string]string `yaml:"headers"`
	SchemaValidation bool              `yaml:"schema_validation"`
}

func DefaultSystem() *SystemConfig {
	return &SystemConfig{
		DefinitionsDir: "./definitions",
		LLM: LLMConf{
			BaseURL:      "https://openrouter.ai/api/v1",
			APIKey:       os.Getenv("OPENROUTER_API_KEY"),
			DefaultModel: "openai/gpt-4o",
			Headers: map[string]string{
				"HTTP-Referer": "https://github.com/angoo/agent-temporal-worker",
				"X-Title":      "agent-temporal-worker",
			},
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

	cfg.LLM.APIKey = expandEnvVar(cfg.LLM.APIKey)

	if cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = os.Getenv("OPENROUTER_API_KEY")
	}

	for k, v := range cfg.LLM.Headers {
		cfg.LLM.Headers[k] = expandEnvVar(v)
	}

	for i := range cfg.MCPServers {
		for k, v := range cfg.MCPServers[i].Headers {
			cfg.MCPServers[i].Headers[k] = expandEnvVar(v)
		}
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
