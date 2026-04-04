# agentfoundry-worker

A Temporal worker that runs AI agents defined in YAML. Each agent is backed by an OpenAI-compatible LLM API and can invoke tools from external MCP servers. The multi-turn conversation loop runs as a durable Temporal workflow, making every LLM call and tool invocation replayable and resilient to failures.

## Architecture

```
                     ┌──────────────────────────────────────┐
                     │        Temporal Frontend             │
                     │        (task queue: agentfoundry-worker)
                     └──────────────┬───────────────────────┘
                                    │
                     ┌──────────────▼───────────────────────┐
                     │           Temporal Worker            │
                     │                                      │
                     │  RunAgentWorkflow                    │
                     │    ├── ResolveAgentActivity          │
                     │    ├── ConnectEphemeralActivity      │
                     │    ├── BuildToolDefsActivity         │
                     │    ├── LLMChatActivity               │
                     │    ├── CallToolActivity              │
                     │    └── CallEphemeralToolActivity     │
                     │                                      │
                     │  Dependencies:                       │
                     │    ├── Registry (agent definitions)  │
                     │    ├── MCP Client Pool               │
                     │    └── LLM Client                    │
                     └──────────────────────────────────────┘
```

## Concepts

**Agent** — A YAML definition combining a system prompt, an LLM model, and a set of tools. When invoked, the agent runs a multi-turn LLM conversation, calling tools as needed, and returns a final response.

**Tool** — A capability from an external MCP server, referenced as `server.tool` (e.g. `srvd.searxng_web_search`). Agents can also call other agents as tools.

**Workflow** — Each agent run executes as a Temporal workflow (`RunAgentWorkflow`). Individual steps (LLM calls, tool invocations) are Temporal activities, making the entire run durable and replayable.

## Quick Start

### Prerequisites

- Go 1.21+
- A running Temporal server
- An API key for any OpenAI-compatible provider

### Build and Run

```bash
go build -o worker ./cmd/worker/
export TEMPORAL_HOST_PORT="localhost:7233"
export OPENROUTER_API_KEY="sk-or-..."
./worker
```

### Docker

```bash
docker build -t agentfoundry-worker .
docker run \
  -e TEMPORAL_HOST_PORT="temporal:7233" \
  -e OPENROUTER_API_KEY="sk-or-..." \
  -v $(pwd)/worker.yaml:/data/worker.yaml \
  -v $(pwd)/definitions:/data/definitions \
  agentfoundry-worker
```

## Configuration

### worker.yaml

```yaml
definitions_dir: "./definitions"

temporal:
  host_port: "localhost:7233"
  namespace: "default"
  # api_key: "${TEMPORAL_API_KEY}"

llm:
  base_url: "https://openrouter.ai/api/v1"
  api_key: "${OPENROUTER_API_KEY}"
  default_model: "openai/gpt-4o"
  headers:
    HTTP-Referer: "https://github.com/angoo/agentfoundry-worker"
    X-Title: "agentfoundry-worker"

mcp_servers:
  - name: "srvd"
    url: "https://mcp.srvd.dev/mcp"
    transport: "streamable-http"
```

### Agent Definition

Agent definitions are YAML files in the `definitions/` directory. They are hot-reloaded when changed.

```yaml
kind: agent
name: researcher
description: "Researches topics by searching the web and summarizing findings"
model: openai/gpt-4o
system_prompt: |
  You are a research assistant. Search the web for information
  and produce well-organized research briefs.
tools:
  - srvd.searxng_web_search
  - srvd.web_url_read
  - summarizer
max_turns: 15
```

### Environment Variables

All config values that accept `${ENV_VAR}` syntax can use env vars. The most common:

| Variable | Description | Default |
|----------|-------------|---------|
| `TEMPORAL_HOST_PORT` | Temporal frontend address (fallback if not in config) | `localhost:7233` |
| `TEMPORAL_API_KEY` | Temporal API key (fallback if not in config) | — |
| `OPENROUTER_API_KEY` | LLM API key (fallback if not in config) | — |

## Project Structure

```
agentfoundry-worker/
├── cmd/worker/main.go           # Worker entrypoint
├── internal/
│   ├── config/                  # System config, agent definitions, YAML loader
│   ├── registry/                # Agent definition store
│   ├── mcpclient/               # MCP client pool (connects to external servers)
│   ├── llm/                     # OpenAI-compatible LLM client
│   └── temporal/                # Temporal workflows and activities
├── definitions/                 # Agent YAML definitions (hot-reloaded)
├── worker.example.yaml          # Example system configuration
├── Dockerfile
└── go.mod
```
