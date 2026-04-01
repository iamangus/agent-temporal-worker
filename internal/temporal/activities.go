package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"go.temporal.io/sdk/activity"

	"github.com/angoo/agent-temporal-worker/internal/config"
	"github.com/angoo/agent-temporal-worker/internal/llm"
	"github.com/angoo/agent-temporal-worker/internal/mcpclient"
	"github.com/angoo/agent-temporal-worker/internal/registry"
)

// Activities holds the dependencies needed by all activity implementations.
// A single instance is registered with the Temporal worker.
type Activities struct {
	registry  *registry.Registry
	pool      *mcpclient.Pool
	llmClient llm.Client
}

// NewActivities creates a new Activities instance.
func NewActivities(reg *registry.Registry, pool *mcpclient.Pool, llmClient llm.Client) *Activities {
	return &Activities{
		registry:  reg,
		pool:      pool,
		llmClient: llmClient,
	}
}

// ResolveAgentActivity looks up an agent definition by name from the registry.
func (a *Activities) ResolveAgentActivity(ctx context.Context, input ResolveAgentInput) (ResolveAgentResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("resolving agent", "agent", input.AgentName)

	def, ok := a.registry.GetAgentDef(input.AgentName)
	if !ok {
		return ResolveAgentResult{}, fmt.Errorf("agent %q not found in registry", input.AgentName)
	}
	return ResolveAgentResult{Definition: def}, nil
}

// ConnectEphemeralActivity connects to one or more ephemeral MCP servers,
// discovers their tools, then closes the connections and returns the tool
// metadata as serialisable structs. The workflow uses this metadata to build
// the tool set; actual tool calls go through CallToolActivity which opens its
// own short-lived connection.
func (a *Activities) ConnectEphemeralActivity(ctx context.Context, input ConnectEphemeralInput) (ConnectEphemeralResult, error) {
	logger := activity.GetLogger(ctx)

	var tools []EphemeralTool
	for _, srv := range input.Servers {
		logger.Info("connecting to ephemeral MCP server", "name", srv.Name, "url", srv.URL)

		conn, err := mcpclient.ConnectEphemeral(ctx, srv)
		if err != nil {
			return ConnectEphemeralResult{}, fmt.Errorf("connect ephemeral server %q: %w", srv.Name, err)
		}

		for _, dt := range conn.ListTools() {
			schema, err := json.Marshal(dt.Tool.InputSchema)
			if err != nil {
				schema = []byte(`{"type":"object"}`)
			}
			tools = append(tools, EphemeralTool{
				ServerName:  dt.ServerName,
				ToolName:    dt.Tool.Name,
				Description: dt.Tool.Description,
				InputSchema: schema,
			})
		}
		conn.Close()
	}

	logger.Info("ephemeral tools discovered", "count", len(tools))
	return ConnectEphemeralResult{Tools: tools}, nil
}

// CallToolActivity calls an MCP tool via the global pool.
func (a *Activities) CallToolActivity(ctx context.Context, input CallToolInput) (CallToolResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("calling MCP tool", "server", input.ServerName, "tool", input.ToolName)

	result, err := a.pool.CallTool(ctx, input.ServerName, input.ToolName, input.Arguments)
	if err != nil {
		return CallToolResult{}, fmt.Errorf("call tool %s.%s: %w", input.ServerName, input.ToolName, err)
	}

	content := extractTextFromMCP(result)
	if result.IsError {
		return CallToolResult{Content: content, IsError: true}, nil
	}

	logger.Info("MCP tool completed", "server", input.ServerName, "tool", input.ToolName, "result_len", len(content))
	return CallToolResult{Content: content}, nil
}

// CallEphemeralToolInput is the input to CallEphemeralToolActivity.
type CallEphemeralToolInput struct {
	Server    mcpclient.ServerConfig `json:"server"`
	ToolName  string                 `json:"tool_name"`
	Arguments map[string]any         `json:"arguments"`
}

// CallEphemeralToolActivity calls a tool on an ephemeral MCP server by opening
// a fresh connection. This keeps activity calls stateless and replayable.
func (a *Activities) CallEphemeralToolActivity(ctx context.Context, input CallEphemeralToolInput) (CallToolResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("calling ephemeral MCP tool", "server", input.Server.Name, "tool", input.ToolName)

	conn, err := mcpclient.ConnectEphemeral(ctx, input.Server)
	if err != nil {
		return CallToolResult{}, fmt.Errorf("connect ephemeral server %q: %w", input.Server.Name, err)
	}
	defer conn.Close()

	result, err := conn.CallTool(ctx, input.ToolName, input.Arguments)
	if err != nil {
		return CallToolResult{}, fmt.Errorf("call ephemeral tool %s.%s: %w", input.Server.Name, input.ToolName, err)
	}

	content := extractTextFromMCP(result)
	if result.IsError {
		return CallToolResult{Content: content, IsError: true}, nil
	}

	return CallToolResult{Content: content}, nil
}

// LLMChatActivity sends a single chat completion request to the LLM.
func (a *Activities) LLMChatActivity(ctx context.Context, input LLMChatInput) (LLMChatResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("sending LLM chat request", "model", input.Request.Model, "messages", len(input.Request.Messages))

	resp, err := a.llmClient.ChatCompletion(ctx, input.Request)
	if err != nil {
		return LLMChatResult{}, fmt.Errorf("LLM chat completion: %w", err)
	}

	return LLMChatResult{Response: resp}, nil
}

// LLMSupportsSchemaActivity is an activity wrapper so the workflow can
// deterministically query whether the LLM client supports native schema validation.
func (a *Activities) LLMSupportsSchemaActivity(ctx context.Context) (bool, error) {
	return a.llmClient.SupportsSchemaValidation(), nil
}

// BuildToolDefsActivity resolves an agent's tool list into LLM tool definitions
// and a lookup table mapping LLM function names to tool routing info.
func (a *Activities) BuildToolDefsActivity(ctx context.Context, input BuildToolDefsInput) (BuildToolDefsResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("building tool set", "agent", input.Definition.Name)

	var toolDefs []llm.ToolDef
	var toolRoutes []ToolRoute

	// Static tools from the agent definition.
	for _, ref := range input.Definition.Tools {
		serverName, toolName, isMCP := parseToolRef(ref)
		if isMCP {
			dt, found := a.pool.GetTool(serverName, toolName)
			if !found {
				logger.Warn("agent references unknown MCP tool, skipping", "agent", input.Definition.Name, "ref", ref)
				continue
			}
			llmName := serverName + "__" + toolName
			toolDefs = append(toolDefs, llm.ToolDef{
				Type: "function",
				Function: llm.FunctionDef{
					Name:        llmName,
					Description: dt.Tool.Description,
					Parameters:  dt.InputSchemaJSON(),
				},
			})
			toolRoutes = append(toolRoutes, ToolRoute{
				LLMName:    llmName,
				ServerName: serverName,
				ToolName:   toolName,
				Kind:       ToolKindMCP,
			})
			continue
		}

		// Agent-as-tool.
		agentDef, ok := a.registry.GetAgentDef(ref)
		if !ok {
			logger.Warn("agent references unresolvable tool/agent, skipping", "agent", input.Definition.Name, "ref", ref)
			continue
		}
		schema := json.RawMessage(`{"type":"object","properties":{"message":{"type":"string","description":"The message/request to send to this agent"}},"required":["message"]}`)
		toolDefs = append(toolDefs, llm.ToolDef{
			Type: "function",
			Function: llm.FunctionDef{
				Name:        ref,
				Description: agentDef.Description,
				Parameters:  schema,
			},
		})
		toolRoutes = append(toolRoutes, ToolRoute{
			LLMName:   ref,
			AgentName: ref,
			Kind:      ToolKindAgent,
		})
	}

	// Ephemeral tools passed in from ConnectEphemeralActivity.
	for _, et := range input.EphemeralTools {
		llmName := et.ServerName + "__" + et.ToolName
		toolDefs = append(toolDefs, llm.ToolDef{
			Type: "function",
			Function: llm.FunctionDef{
				Name:        llmName,
				Description: et.Description,
				Parameters:  json.RawMessage(et.InputSchema),
			},
		})
		toolRoutes = append(toolRoutes, ToolRoute{
			LLMName:    llmName,
			ServerName: et.ServerName,
			ToolName:   et.ToolName,
			Kind:       ToolKindEphemeral,
		})
	}

	logger.Info("tool set built", "agent", input.Definition.Name, "tools", len(toolDefs))
	return BuildToolDefsResult{ToolDefs: toolDefs, ToolRoutes: toolRoutes}, nil
}

// BuildToolDefsInput is the input to BuildToolDefsActivity.
type BuildToolDefsInput struct {
	Definition     *config.Definition `json:"definition"`
	EphemeralTools []EphemeralTool    `json:"ephemeral_tools,omitempty"`
}

// BuildToolDefsResult is the output of BuildToolDefsActivity.
type BuildToolDefsResult struct {
	ToolDefs   []llm.ToolDef `json:"tool_defs"`
	ToolRoutes []ToolRoute   `json:"tool_routes"`
}

// ToolKind identifies how a tool should be dispatched.
type ToolKind string

const (
	ToolKindMCP       ToolKind = "mcp"
	ToolKindEphemeral ToolKind = "ephemeral"
	ToolKindAgent     ToolKind = "agent"
)

// ToolRoute maps an LLM function name to its dispatch destination.
type ToolRoute struct {
	LLMName    string   `json:"llm_name"`
	Kind       ToolKind `json:"kind"`
	ServerName string   `json:"server_name,omitempty"` // MCP or ephemeral
	ToolName   string   `json:"tool_name,omitempty"`   // MCP or ephemeral
	AgentName  string   `json:"agent_name,omitempty"`  // agent-as-tool
}

// parseToolRef splits "server.tool" into its parts.
// Returns false if the ref doesn't contain a dot.
func parseToolRef(ref string) (serverName, toolName string, ok bool) {
	idx := strings.Index(ref, ".")
	if idx < 0 {
		return "", "", false
	}
	return ref[:idx], ref[idx+1:], true
}

// extractTextFromMCP extracts all text content from an MCP CallToolResult.
func extractTextFromMCP(result *mcp.CallToolResult) string {
	var parts []string
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
