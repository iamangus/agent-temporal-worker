package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.temporal.io/sdk/activity"

	"github.com/angoo/agentfoundry-worker/internal/config"
	"github.com/angoo/agentfoundry-worker/internal/llm"
	"github.com/angoo/agentfoundry-worker/internal/orchestrator"
)

type Activities struct {
	orchClient *orchestrator.Client
	llmClient  llm.Client
}

func NewActivities(orchClient *orchestrator.Client, llmClient llm.Client) *Activities {
	return &Activities{
		orchClient: orchClient,
		llmClient:  llmClient,
	}
}

func (a *Activities) ResolveAgentActivity(ctx context.Context, input ResolveAgentInput) (ResolveAgentResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("resolving agent", "agent", input.AgentName)

	def, err := a.orchClient.GetAgent(ctx, input.AgentName)
	if err != nil {
		return ResolveAgentResult{}, fmt.Errorf("agent %q not found: %w", input.AgentName, err)
	}
	return ResolveAgentResult{Definition: def}, nil
}

func (a *Activities) CallToolActivity(ctx context.Context, input CallToolInput) (CallToolResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("calling MCP tool", "server", input.ServerName, "tool", input.ToolName)

	if input.StreamID != "" {
		status := fmt.Sprintf("Running %s.%s", input.ServerName, input.ToolName)
		if perr := a.orchClient.PublishEvent(ctx, input.StreamID, "status", status); perr != nil {
			logger.Warn("failed to publish status", "stream_id", input.StreamID, "error", perr)
		}
	}

	content, isError, err := a.orchClient.CallTool(ctx, input.ServerName, input.ToolName, input.Arguments)
	if err != nil {
		return CallToolResult{}, fmt.Errorf("call tool %s.%s: %w", input.ServerName, input.ToolName, err)
	}

	logger.Info("MCP tool completed", "server", input.ServerName, "tool", input.ToolName, "result_len", len(content))
	return CallToolResult{Content: content, IsError: isError}, nil
}

func (a *Activities) LLMChatActivity(ctx context.Context, input LLMChatInput) (LLMChatResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("sending LLM chat request", "model", input.Request.Model, "messages", len(input.Request.Messages), "stream_id", input.StreamID)

	var resp *llm.ChatResponse
	var err error

	if input.StreamID != "" {
		if perr := a.orchClient.PublishEvent(ctx, input.StreamID, "status", "Thinking"); perr != nil {
			logger.Warn("failed to publish status", "stream_id", input.StreamID, "error", perr)
		}
		var responseStarted bool
		resp, err = a.llmClient.ChatCompletionStream(ctx, input.Request, func(chunk llm.StreamChunk) {
			for _, choice := range chunk.Choices {
				if choice.Index != 0 || choice.Delta.Content == nil {
					continue
				}
				token := *choice.Delta.Content
				if token == "" {
					continue
				}
				if !responseStarted {
					responseStarted = true
					if perr := a.orchClient.PublishEvent(ctx, input.StreamID, "response_start", ""); perr != nil {
						logger.Warn("failed to publish response_start", "stream_id", input.StreamID, "error", perr)
					}
				}
				if perr := a.orchClient.PublishToken(ctx, input.StreamID, token); perr != nil {
					logger.Warn("failed to publish stream token", "stream_id", input.StreamID, "error", perr)
				}
			}
		})
	} else {
		resp, err = a.llmClient.ChatCompletion(ctx, input.Request)
	}

	if err != nil {
		return LLMChatResult{}, fmt.Errorf("LLM chat completion: %w", err)
	}

	return LLMChatResult{Response: resp}, nil
}

func (a *Activities) LLMSupportsSchemaActivity(ctx context.Context) (bool, error) {
	return a.llmClient.SupportsSchemaValidation(), nil
}

func (a *Activities) BuildToolDefsActivity(ctx context.Context, input BuildToolDefsInput) (BuildToolDefsResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("building tool set", "agent", input.Definition.Name)

	tools, err := a.orchClient.ListTools(ctx)
	if err != nil {
		return BuildToolDefsResult{}, fmt.Errorf("list tools from orchestrator: %w", err)
	}

	toolByQualifiedName := make(map[string]orchestrator.ToolInfo, len(tools))
	for _, t := range tools {
		toolByQualifiedName[t.QualifiedName] = t
	}

	var toolDefs []llm.ToolDef
	var toolRoutes []ToolRoute

	for _, ref := range input.Definition.Tools {
		serverName, toolName, isMCP := parseToolRef(ref)
		if isMCP {
			ti, found := toolByQualifiedName[ref]
			if !found {
				logger.Warn("agent references unknown MCP tool, skipping", "agent", input.Definition.Name, "ref", ref)
				continue
			}
			llmName := serverName + "__" + toolName
			var params json.RawMessage
			if ti.InputSchema != nil {
				params = ti.InputSchema
			} else {
				params = json.RawMessage(`{"type":"object"}`)
			}
			toolDefs = append(toolDefs, llm.ToolDef{
				Type: "function",
				Function: llm.FunctionDef{
					Name:        llmName,
					Description: ti.Description,
					Parameters:  params,
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

		agentDef, err := a.orchClient.GetAgent(ctx, ref)
		if err != nil {
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

	logger.Info("tool set built", "agent", input.Definition.Name, "tools", len(toolDefs))
	return BuildToolDefsResult{ToolDefs: toolDefs, ToolRoutes: toolRoutes}, nil
}

type BuildToolDefsInput struct {
	Definition *config.Definition `json:"definition"`
}

type BuildToolDefsResult struct {
	ToolDefs   []llm.ToolDef `json:"tool_defs"`
	ToolRoutes []ToolRoute   `json:"tool_routes"`
}

func parseToolRef(ref string) (serverName, toolName string, ok bool) {
	idx := strings.Index(ref, ".")
	if idx < 0 {
		return "", "", false
	}
	return ref[:idx], ref[idx+1:], true
}
