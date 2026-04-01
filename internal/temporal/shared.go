package temporal

import (
	"github.com/angoo/agent-temporal-worker/internal/config"
	"github.com/angoo/agent-temporal-worker/internal/llm"
	"github.com/angoo/agent-temporal-worker/internal/mcpclient"
)

const (
	// TaskQueue is the Temporal task queue name for this worker.
	TaskQueue = "agent-temporal-worker"

	// WorkflowType is the registered name of the main agent workflow.
	WorkflowType = "RunAgentWorkflow"
)

// RunAgentParams is the input to RunAgentWorkflow.
type RunAgentParams struct {
	// AgentName is the name of the agent definition to load from the registry.
	AgentName string `json:"agent_name"`

	// Message is the user input for this run.
	Message string `json:"message"`

	// History contains prior conversation turns (user/assistant pairs).
	// Prepended between the system prompt and the new user message.
	History []llm.Message `json:"history,omitempty"`

	// MCPServers are ephemeral MCP servers to connect for this run only.
	// Their tools are merged with the agent's statically configured tools.
	MCPServers []mcpclient.ServerConfig `json:"mcp_servers,omitempty"`

	// ResponseSchema overrides the agent definition's structured_output for
	// this run. Ignored if nil.
	ResponseSchema *config.StructuredOutput `json:"response_schema,omitempty"`
}

// RunAgentResult is the output of RunAgentWorkflow.
type RunAgentResult struct {
	// Response is the final text (or JSON) response from the agent.
	Response string `json:"response"`

	// History is the full updated message history (excluding the system prompt),
	// suitable for passing back as RunAgentParams.History on the next turn.
	History []llm.Message `json:"history,omitempty"`
}

// ResolveAgentInput is the input to the ResolveAgentActivity.
type ResolveAgentInput struct {
	AgentName string `json:"agent_name"`
}

// ResolveAgentResult is the output of the ResolveAgentActivity.
type ResolveAgentResult struct {
	Definition *config.Definition `json:"definition"`
}

// ConnectEphemeralInput is the input to the ConnectEphemeralActivity.
type ConnectEphemeralInput struct {
	Servers []mcpclient.ServerConfig `json:"servers"`
}

// EphemeralTool is a serialisable description of a tool from an ephemeral
// MCP connection, used to reconstruct tool definitions inside the workflow
// without holding a live connection across activity boundaries.
type EphemeralTool struct {
	ServerName  string `json:"server_name"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	// InputSchema is the raw JSON Schema for the tool's input.
	InputSchema []byte `json:"input_schema"`
}

// ConnectEphemeralResult is the output of the ConnectEphemeralActivity.
type ConnectEphemeralResult struct {
	Tools []EphemeralTool `json:"tools"`
}

// LLMChatInput is the input to the LLMChatActivity.
type LLMChatInput struct {
	Request *llm.ChatRequest `json:"request"`
}

// LLMChatResult is the output of the LLMChatActivity.
type LLMChatResult struct {
	Response *llm.ChatResponse `json:"response"`
}

// CallToolInput is the input to the CallToolActivity.
type CallToolInput struct {
	// ServerName and ToolName identify the target MCP tool in the global pool.
	ServerName string `json:"server_name"`
	ToolName   string `json:"tool_name"`
	// Arguments are the parsed JSON arguments for the tool call.
	Arguments map[string]any `json:"arguments"`
}

// CallToolResult is the output of the CallToolActivity.
type CallToolResult struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}
