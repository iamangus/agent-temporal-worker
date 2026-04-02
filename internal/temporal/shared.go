package temporal

import (
	"github.com/angoo/agent-temporal-worker/internal/config"
	"github.com/angoo/agent-temporal-worker/internal/llm"
)

const (
	TaskQueue    = "agent-temporal-worker"
	WorkflowType = "RunAgentWorkflow"
)

type RunAgentParams struct {
	AgentName      string                   `json:"agent_name"`
	Message        string                   `json:"message"`
	History        []llm.Message            `json:"history,omitempty"`
	ResponseSchema *config.StructuredOutput `json:"response_schema,omitempty"`
}

type RunAgentResult struct {
	Response string        `json:"response"`
	History  []llm.Message `json:"history,omitempty"`
}

type ResolveAgentInput struct {
	AgentName string `json:"agent_name"`
}

type ResolveAgentResult struct {
	Definition *config.Definition `json:"definition"`
}

type LLMChatInput struct {
	Request *llm.ChatRequest `json:"request"`
}

type LLMChatResult struct {
	Response *llm.ChatResponse `json:"response"`
}

type CallToolInput struct {
	ServerName string         `json:"server_name"`
	ToolName   string         `json:"tool_name"`
	Arguments  map[string]any `json:"arguments"`
}

type CallToolResult struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

type ToolKind string

const (
	ToolKindMCP   ToolKind = "mcp"
	ToolKindAgent ToolKind = "agent"
)

type ToolRoute struct {
	LLMName    string   `json:"llm_name"`
	Kind       ToolKind `json:"kind"`
	ServerName string   `json:"server_name,omitempty"`
	ToolName   string   `json:"tool_name,omitempty"`
	AgentName  string   `json:"agent_name,omitempty"`
}
