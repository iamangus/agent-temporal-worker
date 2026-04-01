package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// DiscoveredTool represents a tool discovered from an external MCP server.
type DiscoveredTool struct {
	// ServerName is the name of the MCP server this tool belongs to.
	ServerName string
	// Tool is the MCP tool metadata.
	Tool mcp.Tool
}

// QualifiedName returns the namespaced name: "server.tool".
func (dt *DiscoveredTool) QualifiedName() string {
	return dt.ServerName + "." + dt.Tool.Name
}

// InputSchemaJSON returns the tool's input schema as json.RawMessage.
func (dt *DiscoveredTool) InputSchemaJSON() json.RawMessage {
	data, err := json.Marshal(dt.Tool.InputSchema)
	if err != nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	return data
}

// Transport type constants.
const (
	TransportSSE            = "sse"
	TransportStreamableHTTP = "streamable-http"
)

// ServerConfig holds the configuration for connecting to an external MCP server.
type ServerConfig struct {
	Name      string            `yaml:"name" json:"name"`
	URL       string            `yaml:"url" json:"url"`
	Transport string            `yaml:"transport" json:"transport"` // "sse" (default) or "streamable-http"
	Headers   map[string]string `yaml:"headers" json:"headers"`
}

// connection holds a live MCP client connection and its discovered tools.
type connection struct {
	client *client.Client
	config ServerConfig
	tools  []mcp.Tool
}

// Pool manages connections to external MCP servers and provides
// tool discovery and proxied tool invocation.
type Pool struct {
	mu    sync.RWMutex
	conns map[string]*connection
}

// NewPool creates a new MCP client pool.
func NewPool() *Pool {
	return &Pool{
		conns: make(map[string]*connection),
	}
}

// Connect establishes connections to all configured MCP servers,
// initializes sessions, and discovers tools.
func (p *Pool) Connect(ctx context.Context, servers []ServerConfig) error {
	for _, srv := range servers {
		if err := p.connectOne(ctx, srv); err != nil {
			slog.Error("failed to connect to MCP server", "name", srv.Name, "url", srv.URL, "error", err)
			// Continue connecting to other servers; don't fail hard.
			continue
		}
	}
	return nil
}

// connectOne connects to a single MCP server.
func (p *Pool) connectOne(ctx context.Context, srv ServerConfig) error {
	transport := srv.Transport
	if transport == "" {
		transport = TransportSSE
	}
	slog.Info("connecting to MCP server", "name", srv.Name, "url", srv.URL, "transport", transport)

	var c *client.Client
	var err error

	switch transport {
	case TransportSSE:
		var opts []mcptransport.ClientOption
		if len(srv.Headers) > 0 {
			opts = append(opts, client.WithHeaders(srv.Headers))
		}
		c, err = client.NewSSEMCPClient(srv.URL, opts...)
	case TransportStreamableHTTP:
		var opts []mcptransport.StreamableHTTPCOption
		if len(srv.Headers) > 0 {
			opts = append(opts, mcptransport.WithHTTPHeaders(srv.Headers))
		}
		c, err = client.NewStreamableHttpClient(srv.URL, opts...)
	default:
		return fmt.Errorf("unknown transport %q for server %s (use 'sse' or 'streamable-http')", transport, srv.Name)
	}
	if err != nil {
		return fmt.Errorf("create %s client for %s: %w", transport, srv.Name, err)
	}

	conn := &connection{
		client: c,
		config: srv,
	}

	// Register notification handler before Start so we don't miss any.
	serverName := srv.Name
	c.OnNotification(func(notification mcp.JSONRPCNotification) {
		if notification.Method == mcp.MethodNotificationToolsListChanged {
			slog.Info("tool list changed notification received", "server", serverName)
		}
	})

	// Start the transport.
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := c.Start(startCtx); err != nil {
		c.Close()
		return fmt.Errorf("start %s client for %s: %w", transport, srv.Name, err)
	}

	// Initialize the MCP session.
	_, err = c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "agent-temporal-worker",
				Version: "0.1.0",
			},
			Capabilities: mcp.ClientCapabilities{},
		},
	})
	if err != nil {
		c.Close()
		return fmt.Errorf("initialize MCP session for %s: %w", srv.Name, err)
	}

	// Discover tools.
	toolsResult, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		c.Close()
		return fmt.Errorf("list tools from %s: %w", srv.Name, err)
	}

	conn.tools = toolsResult.Tools

	p.mu.Lock()
	p.conns[srv.Name] = conn
	p.mu.Unlock()

	toolNames := make([]string, len(conn.tools))
	for i, t := range conn.tools {
		toolNames[i] = t.Name
	}
	slog.Info("connected to MCP server", "name", srv.Name, "tools", toolNames)

	return nil
}

// GetTool looks up a tool by its qualified name ("server.tool").
func (p *Pool) GetTool(serverName, toolName string) (*DiscoveredTool, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	conn, ok := p.conns[serverName]
	if !ok {
		return nil, false
	}

	for _, t := range conn.tools {
		if t.Name == toolName {
			return &DiscoveredTool{
				ServerName: serverName,
				Tool:       t,
			}, true
		}
	}
	return nil, false
}

// CallTool invokes a tool on the appropriate external MCP server.
func (p *Pool) CallTool(ctx context.Context, serverName, toolName string, arguments map[string]any) (*mcp.CallToolResult, error) {
	p.mu.RLock()
	conn, ok := p.conns[serverName]
	p.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown MCP server: %s", serverName)
	}

	result, err := conn.client.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: arguments,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("call tool %s.%s: %w", serverName, toolName, err)
	}

	return result, nil
}

// EphemeralConn is a short-lived connection to a single MCP server, intended
// for use within a single agent run. It is not registered in the global Pool
// and must be closed by the caller when the run completes.
type EphemeralConn struct {
	config ServerConfig
	client *client.Client
	tools  []mcp.Tool
}

// ConnectEphemeral connects to a single MCP server outside of the global pool
// and returns an EphemeralConn. The caller is responsible for calling Close
// when the connection is no longer needed.
func ConnectEphemeral(ctx context.Context, srv ServerConfig) (*EphemeralConn, error) {
	transport := srv.Transport
	if transport == "" {
		transport = TransportSSE
	}

	var c *client.Client
	var err error

	switch transport {
	case TransportSSE:
		var opts []mcptransport.ClientOption
		if len(srv.Headers) > 0 {
			opts = append(opts, client.WithHeaders(srv.Headers))
		}
		c, err = client.NewSSEMCPClient(srv.URL, opts...)
	case TransportStreamableHTTP:
		var opts []mcptransport.StreamableHTTPCOption
		if len(srv.Headers) > 0 {
			opts = append(opts, mcptransport.WithHTTPHeaders(srv.Headers))
		}
		c, err = client.NewStreamableHttpClient(srv.URL, opts...)
	default:
		return nil, fmt.Errorf("unknown transport %q for server %s (use 'sse' or 'streamable-http')", transport, srv.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("create %s client for %s: %w", transport, srv.Name, err)
	}

	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := c.Start(startCtx); err != nil {
		c.Close()
		return nil, fmt.Errorf("start %s client for %s: %w", transport, srv.Name, err)
	}

	_, err = c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "agent-temporal-worker",
				Version: "0.1.0",
			},
			Capabilities: mcp.ClientCapabilities{},
		},
	})
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize MCP session for %s: %w", srv.Name, err)
	}

	toolsResult, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("list tools from %s: %w", srv.Name, err)
	}

	toolNames := make([]string, len(toolsResult.Tools))
	for i, t := range toolsResult.Tools {
		toolNames[i] = t.Name
	}
	slog.Info("ephemeral MCP connection established", "name", srv.Name, "tools", toolNames)

	return &EphemeralConn{
		config: srv,
		client: c,
		tools:  toolsResult.Tools,
	}, nil
}

// ServerName returns the name this server was registered under.
func (e *EphemeralConn) ServerName() string {
	return e.config.Name
}

// ListTools returns all tools discovered from this ephemeral server.
func (e *EphemeralConn) ListTools() []DiscoveredTool {
	tools := make([]DiscoveredTool, len(e.tools))
	for i, t := range e.tools {
		tools[i] = DiscoveredTool{
			ServerName: e.config.Name,
			Tool:       t,
		}
	}
	return tools
}

// CallTool invokes a tool on this ephemeral server.
func (e *EphemeralConn) CallTool(ctx context.Context, toolName string, arguments map[string]any) (*mcp.CallToolResult, error) {
	return e.client.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: arguments,
		},
	})
}

// Close shuts down the ephemeral MCP connection.
func (e *EphemeralConn) Close() {
	slog.Info("closing ephemeral MCP connection", "server", e.config.Name)
	e.client.Close()
}

// Close shuts down all MCP client connections.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for name, conn := range p.conns {
		slog.Info("closing MCP client connection", "server", name)
		conn.client.Close()
	}
	p.conns = make(map[string]*connection)
}
