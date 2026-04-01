package temporal

import (
	"encoding/json"
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/angoo/agent-temporal-worker/internal/llm"
	"github.com/angoo/agent-temporal-worker/internal/mcpclient"
)

// defaultActivityOptions are the default retry/timeout settings for activities.
var defaultActivityOptions = workflow.ActivityOptions{
	StartToCloseTimeout: 2 * time.Minute,
	RetryPolicy: &temporal.RetryPolicy{
		MaximumAttempts: 5,
	},
}

// llmActivityOptions are more generous for LLM calls which can be slow.
var llmActivityOptions = workflow.ActivityOptions{
	StartToCloseTimeout: 5 * time.Minute,
	RetryPolicy: &temporal.RetryPolicy{
		MaximumAttempts:        10,
		InitialInterval:        time.Second,
		BackoffCoefficient:     2.0,
		MaximumInterval:        10 * time.Second,
		NonRetryableErrorTypes: []string{"NonRetryable"},
	},
}

// RunAgentWorkflow is the main Temporal workflow for running an agent.
// It mirrors the multi-turn LLM conversation loop previously in agent.Runtime.RunWithHistory,
// but with each LLM call and tool call executed as activities, making the
// entire run durable and replayable.
func RunAgentWorkflow(ctx workflow.Context, params RunAgentParams) (RunAgentResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("starting agent workflow", "agent", params.AgentName)

	actCtx := workflow.WithActivityOptions(ctx, defaultActivityOptions)

	// 1. Resolve the agent definition.
	var resolveResult ResolveAgentResult
	err := workflow.ExecuteActivity(actCtx, (*Activities).ResolveAgentActivity, ResolveAgentInput{
		AgentName: params.AgentName,
	}).Get(ctx, &resolveResult)
	if err != nil {
		return RunAgentResult{}, fmt.Errorf("resolve agent: %w", err)
	}
	def := resolveResult.Definition

	// 2. Connect to any ephemeral MCP servers and discover their tools.
	var ephemeralTools []EphemeralTool
	if len(params.MCPServers) > 0 {
		var ephResult ConnectEphemeralResult
		err = workflow.ExecuteActivity(actCtx, (*Activities).ConnectEphemeralActivity, ConnectEphemeralInput{
			Servers: params.MCPServers,
		}).Get(ctx, &ephResult)
		if err != nil {
			return RunAgentResult{}, fmt.Errorf("connect ephemeral servers: %w", err)
		}
		ephemeralTools = ephResult.Tools
	}

	// 3. Build the tool set (LLM tool definitions + routing table).
	var toolDefsResult BuildToolDefsResult
	err = workflow.ExecuteActivity(actCtx, (*Activities).BuildToolDefsActivity, BuildToolDefsInput{
		Definition:     def,
		EphemeralTools: ephemeralTools,
	}).Get(ctx, &toolDefsResult)
	if err != nil {
		return RunAgentResult{}, fmt.Errorf("build tool defs: %w", err)
	}

	// Build a route lookup map for fast dispatch.
	routeByLLMName := make(map[string]ToolRoute, len(toolDefsResult.ToolRoutes))
	for _, r := range toolDefsResult.ToolRoutes {
		routeByLLMName[r.LLMName] = r
	}

	// Build a lookup map for ephemeral server configs (needed when dispatching ephemeral tools).
	ephemeralServerByName := make(map[string]mcpclient.ServerConfig, len(params.MCPServers))
	for _, srv := range params.MCPServers {
		ephemeralServerByName[srv.Name] = srv
	}

	// 4. Determine structured output / response format.
	so := params.ResponseSchema
	if so == nil {
		so = def.StructuredOutput
	}

	// supportsSchemaValidation is queried once as an activity so the result
	// is durably recorded in the workflow history and replayed deterministically.
	var supportsSchema bool
	if err = workflow.ExecuteActivity(actCtx, (*Activities).LLMSupportsSchemaActivity).Get(ctx, &supportsSchema); err != nil {
		return RunAgentResult{}, fmt.Errorf("query schema support: %w", err)
	}

	// 5. Build initial messages: system prompt + history + user message.
	systemPrompt := def.SystemPrompt
	if so != nil && !supportsSchema {
		systemPrompt += fmt.Sprintf(
			"\n\nYou must respond with ONLY valid JSON matching this schema:\n%s",
			string(so.Schema),
		)
	}

	messages := make([]llm.Message, 0, 2+len(params.History))
	messages = append(messages, llm.Message{Role: "system", Content: systemPrompt})
	messages = append(messages, params.History...)
	messages = append(messages, llm.Message{Role: "user", Content: params.Message})

	maxTurns := def.MaxTurns
	if maxTurns == 0 {
		maxTurns = 10
	}

	llmCtx := workflow.WithActivityOptions(ctx, llmActivityOptions)

	// 6. Multi-turn loop.
	for turn := 0; turn < maxTurns; turn++ {
		logger.Info("agent turn", "agent", def.Name, "turn", turn+1)

		// Build the LLM request.
		req := &llm.ChatRequest{
			Model:    def.Model,
			Messages: messages,
		}
		if len(toolDefsResult.ToolDefs) > 0 {
			req.Tools = toolDefsResult.ToolDefs
		}
		if so != nil && supportsSchema {
			req.ResponseFormat = &llm.ResponseFormat{
				Type: "json_schema",
				JSONSchema: &llm.JSONSchema{
					Name:   so.Name,
					Schema: so.Schema,
					Strict: so.Strict,
				},
			}
		} else if so != nil {
			req.ResponseFormat = &llm.ResponseFormat{Type: "json_object"}
		} else if def.ForceJSON {
			req.ResponseFormat = &llm.ResponseFormat{Type: "json_object"}
		}

		// Call the LLM.
		var llmResult LLMChatResult
		err = workflow.ExecuteActivity(llmCtx, (*Activities).LLMChatActivity, LLMChatInput{Request: req}).
			Get(ctx, &llmResult)
		if err != nil {
			return RunAgentResult{}, fmt.Errorf("LLM call failed on turn %d: %w", turn+1, err)
		}

		resp := llmResult.Response
		if len(resp.Choices) == 0 {
			return RunAgentResult{}, fmt.Errorf("no choices in LLM response on turn %d", turn+1)
		}

		// Merge all index-0 choices (some providers split text and tool_calls).
		var assistantMsg llm.Message
		for _, c := range resp.Choices {
			if c.Index != 0 {
				continue
			}
			if assistantMsg.Role == "" {
				assistantMsg.Role = c.Message.Role
			}
			if assistantMsg.Content == nil {
				assistantMsg.Content = c.Message.Content
			}
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, c.Message.ToolCalls...)
		}
		messages = append(messages, assistantMsg)

		// No tool calls — we have a final response.
		if len(assistantMsg.ToolCalls) == 0 {
			content, _ := assistantMsg.Content.(string)
			if so != nil || def.ForceJSON {
				content = llm.StripCodeFences(content)
			}

			// Validate or retry if schema validation fails.
			if so != nil {
				if verr := llm.ValidateAgainstSchema(content, so.Schema); verr != nil {
					logger.Warn("LLM response failed schema validation, retrying",
						"agent", def.Name, "turn", turn+1, "error", verr)
					var retryMsg string
					if !supportsSchema {
						retryMsg = fmt.Sprintf(
							"Your previous response did not conform to the required JSON schema.\n\n"+
								"Validation error: %s\n\n"+
								"The JSON schema you must follow:\n%s\n\n"+
								"Please try again, returning ONLY valid JSON that matches this schema exactly.",
							verr, string(so.Schema),
						)
					} else {
						retryMsg = fmt.Sprintf(
							"Your previous response did not conform to the required JSON schema. "+
								"Validation error: %s\n\nPlease try again, returning ONLY valid JSON that matches the schema exactly.",
							verr,
						)
					}
					messages = append(messages, llm.Message{Role: "user", Content: retryMsg})
					continue
				}
			} else if def.ForceJSON {
				if jerr := llm.IsValidJSON(content); jerr != nil {
					logger.Warn("LLM response was not valid JSON, retrying",
						"agent", def.Name, "turn", turn+1, "error", jerr)
					messages = append(messages, llm.Message{
						Role: "user",
						Content: fmt.Sprintf(
							"Your previous response was not valid JSON. "+
								"Parse error: %s\n\nPlease try again, returning ONLY valid JSON.",
							jerr,
						),
					})
					continue
				}
			}

			logger.Info("agent workflow completed", "agent", def.Name, "turns", turn+1)
			// Return history without the system prompt.
			return RunAgentResult{
				Response: content,
				History:  messages[1:],
			}, nil
		}

		// Dispatch tool calls.
		// Parallel execution uses workflow.Go goroutines (deterministic).
		type toolCallOutcome struct {
			toolCallID string
			content    string
		}

		outcomes := make([]toolCallOutcome, len(assistantMsg.ToolCalls))
		errors := make([]error, len(assistantMsg.ToolCalls))

		// Determine concurrency limit.
		maxConcurrent := def.MaxConcurrentTools
		sem := make(chan struct{}, func() int {
			if maxConcurrent > 0 {
				return maxConcurrent
			}
			return len(assistantMsg.ToolCalls) // unlimited
		}())

		wg := workflow.NewWaitGroup(ctx)
		for i, tc := range assistantMsg.ToolCalls {
			i, tc := i, tc
			wg.Add(1)
			workflow.Go(ctx, func(ctx workflow.Context) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				content, err := dispatchToolCall(ctx, tc, routeByLLMName, ephemeralServerByName, params)
				outcomes[i] = toolCallOutcome{toolCallID: tc.ID, content: content}
				errors[i] = err
			})
		}
		wg.Wait(ctx)

		// Append tool results to the conversation.
		for i, outcome := range outcomes {
			content := outcome.content
			if errors[i] != nil {
				logger.Warn("tool call failed", "agent", def.Name, "tool", assistantMsg.ToolCalls[i].Function.Name, "error", errors[i])
				content = fmt.Sprintf("Error: %s", errors[i].Error())
			}
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: outcome.toolCallID,
			})
		}
	}

	return RunAgentResult{}, fmt.Errorf("agent %s exceeded max turns (%d)", def.Name, maxTurns)
}

// dispatchToolCall routes a single tool call to the appropriate activity or child workflow.
func dispatchToolCall(
	ctx workflow.Context,
	tc llm.ToolCall,
	routeByLLMName map[string]ToolRoute,
	ephemeralServerByName map[string]mcpclient.ServerConfig,
	params RunAgentParams,
) (string, error) {
	logger := workflow.GetLogger(ctx)

	route, ok := routeByLLMName[tc.Function.Name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", tc.Function.Name)
	}

	switch route.Kind {
	case ToolKindAgent:
		// Sub-agent: launch as a child workflow.
		var agentInput struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &agentInput); err != nil {
			return "", fmt.Errorf("parse agent call input: %w", err)
		}
		logger.Info("dispatching sub-agent", "agent", route.AgentName, "input_len", len(agentInput.Message))

		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			TaskQueue: TaskQueue,
		})
		var childResult RunAgentResult
		err := workflow.ExecuteChildWorkflow(childCtx, RunAgentWorkflow, RunAgentParams{
			AgentName: route.AgentName,
			Message:   agentInput.Message,
			// Sub-agents do not inherit history or ephemeral servers from the parent.
		}).Get(ctx, &childResult)
		if err != nil {
			return "", fmt.Errorf("sub-agent %s failed: %w", route.AgentName, err)
		}
		return childResult.Response, nil

	case ToolKindMCP:
		// MCP tool via the global pool.
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "", fmt.Errorf("parse tool arguments: %w", err)
		}
		logger.Info("dispatching MCP tool", "server", route.ServerName, "tool", route.ToolName)

		actCtx := workflow.WithActivityOptions(ctx, defaultActivityOptions)
		var result CallToolResult
		err := workflow.ExecuteActivity(actCtx, (*Activities).CallToolActivity, CallToolInput{
			ServerName: route.ServerName,
			ToolName:   route.ToolName,
			Arguments:  args,
		}).Get(ctx, &result)
		if err != nil {
			return "", err
		}
		if result.IsError {
			return "", fmt.Errorf("tool returned error: %s", result.Content)
		}
		return result.Content, nil

	case ToolKindEphemeral:
		// Ephemeral MCP tool — open a fresh connection in the activity.
		srv, ok := ephemeralServerByName[route.ServerName]
		if !ok {
			return "", fmt.Errorf("ephemeral server %q not found in params", route.ServerName)
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "", fmt.Errorf("parse ephemeral tool arguments: %w", err)
		}

		actCtx := workflow.WithActivityOptions(ctx, defaultActivityOptions)
		var result CallToolResult
		err := workflow.ExecuteActivity(actCtx, (*Activities).CallEphemeralToolActivity, CallEphemeralToolInput{
			Server:    srv,
			ToolName:  route.ToolName,
			Arguments: args,
		}).Get(ctx, &result)
		if err != nil {
			return "", err
		}
		if result.IsError {
			return "", fmt.Errorf("ephemeral tool returned error: %s", result.Content)
		}
		return result.Content, nil

	default:
		return "", fmt.Errorf("unknown tool kind %q for tool %s", route.Kind, tc.Function.Name)
	}
}
