package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/angoo/agentfoundry-worker/internal/config"
)

type Config struct {
	URL    string `yaml:"url"`
	APIKey string `yaml:"api_key"`
}

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type ToolInfo struct {
	QualifiedName string          `json:"qualified_name"`
	Server        string          `json:"server"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	InputSchema   json.RawMessage `json:"input_schema"`
}

type mcpCallRequest struct {
	Server    string         `json:"server"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

type mcpCallResponse struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

func NewClient(cfg Config) *Client {
	return &Client{
		baseURL: cfg.URL,
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *Client) GetAgent(ctx context.Context, name string) (*config.Definition, error) {
	var def config.Definition
	if err := c.get(ctx, "/api/v1/agents/"+name, &def); err != nil {
		return nil, fmt.Errorf("get agent %q: %w", name, err)
	}
	return &def, nil
}

func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	var tools []ToolInfo
	if err := c.get(ctx, "/api/v1/tools", &tools); err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	return tools, nil
}

func (c *Client) CallTool(ctx context.Context, server, tool string, arguments map[string]any) (string, bool, error) {
	body := mcpCallRequest{
		Server:    server,
		Tool:      tool,
		Arguments: arguments,
	}
	var resp mcpCallResponse
	if err := c.post(ctx, "/api/internal/mcp/call", body, &resp); err != nil {
		return "", false, fmt.Errorf("call tool %s.%s: %w", server, tool, err)
	}
	return resp.Content, resp.IsError, nil
}

func (c *Client) publishShort(ctx context.Context, path string, body any) error {
	shortClient := &http.Client{Timeout: 5 * time.Second}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)
	resp, err := shortClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *Client) PublishToken(ctx context.Context, streamID, token string) error {
	return c.publishShort(ctx, "/api/internal/streams/"+streamID+"/tokens", map[string]string{"token": token})
}

func (c *Client) PublishEvent(ctx context.Context, streamID, eventType, data string) error {
	return c.publishShort(ctx, "/api/internal/streams/"+streamID+"/events", map[string]string{"type": eventType, "data": data})
}

func (c *Client) get(ctx context.Context, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	c.setAuth(req)
	return c.do(req, result)
}

func (c *Client) post(ctx context.Context, path string, body any, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)
	return c.do(req, result)
}

func (c *Client) setAuth(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

func (c *Client) do(req *http.Request, result any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}
