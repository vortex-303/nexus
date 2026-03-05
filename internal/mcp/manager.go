package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerConfig holds the configuration for an MCP server.
type ServerConfig struct {
	ID        string
	Name      string
	Transport string // "stdio" or "sse"
	Command   string // stdio: full command string
	Args      []string
	URL       string // sse: endpoint URL
	Env       map[string]string
	Headers   map[string]string
	Prefix    string // tool namespace prefix
	Enabled   bool
}

// ToolInfo describes a single tool from an MCP server.
type ToolInfo struct {
	ServerID    string          `json:"server_id"`
	ServerName  string          `json:"server_name"`
	OrigName    string          `json:"orig_name"`
	QualName    string          `json:"qual_name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type managedServer struct {
	cfg     ServerConfig
	session *sdkmcp.ClientSession
	tools   []ToolInfo
}

// Manager manages MCP server connections and tool discovery.
type Manager struct {
	mu      sync.RWMutex
	servers map[string]*managedServer
}

// NewManager creates a new MCP Manager.
func NewManager() *Manager {
	return &Manager{
		servers: make(map[string]*managedServer),
	}
}

// Connect establishes a connection to an MCP server and discovers its tools.
func (m *Manager) Connect(ctx context.Context, cfg ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Disconnect existing if any
	if existing, ok := m.servers[cfg.ID]; ok {
		if existing.session != nil {
			existing.session.Close()
		}
		delete(m.servers, cfg.ID)
	}

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "nexus",
		Version: "1.0.0",
	}, nil)

	var transport sdkmcp.Transport

	switch cfg.Transport {
	case "stdio":
		parts := buildCommand(cfg.Command, cfg.Args)
		if len(parts) == 0 {
			return fmt.Errorf("empty command")
		}
		cmd := exec.Command(parts[0], parts[1:]...)
		// Set environment variables
		cmd.Env = os.Environ()
		for k, v := range cfg.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.Stderr = os.Stderr
		transport = &sdkmcp.CommandTransport{Command: cmd}

	case "sse":
		if cfg.URL == "" {
			return fmt.Errorf("URL required for SSE transport")
		}
		transport = &sdkmcp.SSEClientTransport{Endpoint: cfg.URL}

	default:
		return fmt.Errorf("unsupported transport: %s", cfg.Transport)
	}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	ms := &managedServer{
		cfg:     cfg,
		session: session,
	}
	m.servers[cfg.ID] = ms

	// Discover tools
	if err := m.refreshToolsLocked(ctx, ms); err != nil {
		log.Printf("[mcp:%s] tool discovery failed: %v", cfg.Name, err)
		// Keep connection alive, tools may appear later
	}

	log.Printf("[mcp:%s] connected, %d tools discovered", cfg.Name, len(ms.tools))
	return nil
}

// Disconnect closes a server connection.
func (m *Manager) Disconnect(serverID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ms, ok := m.servers[serverID]
	if !ok {
		return nil
	}
	if ms.session != nil {
		ms.session.Close()
	}
	delete(m.servers, serverID)
	return nil
}

// RefreshTools re-discovers tools from a server.
func (m *Manager) RefreshTools(ctx context.Context, serverID string) ([]ToolInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ms, ok := m.servers[serverID]
	if !ok {
		return nil, fmt.Errorf("server %s not connected", serverID)
	}

	if err := m.refreshToolsLocked(ctx, ms); err != nil {
		return nil, err
	}
	return ms.tools, nil
}

func (m *Manager) refreshToolsLocked(ctx context.Context, ms *managedServer) error {
	result, err := ms.session.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("list tools: %w", err)
	}

	ms.tools = nil
	for _, tool := range result.Tools {
		qualName := tool.Name
		if ms.cfg.Prefix != "" {
			qualName = ms.cfg.Prefix + "__" + tool.Name
		}

		// Marshal InputSchema to json.RawMessage
		schemaBytes, err := json.Marshal(tool.InputSchema)
		if err != nil {
			schemaBytes = []byte(`{"type":"object"}`)
		}

		ms.tools = append(ms.tools, ToolInfo{
			ServerID:    ms.cfg.ID,
			ServerName:  ms.cfg.Name,
			OrigName:    tool.Name,
			QualName:    qualName,
			Description: tool.Description,
			InputSchema: schemaBytes,
		})
	}
	return nil
}

// AllTools returns all tools from all connected servers.
func (m *Manager) AllTools() []ToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var all []ToolInfo
	for _, ms := range m.servers {
		all = append(all, ms.tools...)
	}
	return all
}

// ServerTools returns tools for a specific server.
func (m *Manager) ServerTools(serverID string) []ToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ms, ok := m.servers[serverID]
	if !ok {
		return nil
	}
	return ms.tools
}

// CallTool routes a tool call to the correct server and returns the result.
func (m *Manager) CallTool(ctx context.Context, qualifiedName string, args map[string]any) (string, error) {
	m.mu.RLock()

	var targetServer *managedServer
	var origName string

	for _, ms := range m.servers {
		for _, tool := range ms.tools {
			if tool.QualName == qualifiedName {
				targetServer = ms
				origName = tool.OrigName
				break
			}
		}
		if targetServer != nil {
			break
		}
	}
	m.mu.RUnlock()

	if targetServer == nil {
		return "", fmt.Errorf("no MCP server has tool %q", qualifiedName)
	}

	result, err := targetServer.session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      origName,
		Arguments: args,
	})
	if err != nil {
		// Try reconnect once
		reconnCtx, cancel := context.WithTimeout(ctx, 10*secondDuration)
		defer cancel()
		if reconnErr := m.reconnect(reconnCtx, targetServer); reconnErr == nil {
			result, err = targetServer.session.CallTool(ctx, &sdkmcp.CallToolParams{
				Name:      origName,
				Arguments: args,
			})
		}
		if err != nil {
			return "", fmt.Errorf("call tool %s: %w", origName, err)
		}
	}

	return ContentToString(result), nil
}

func (m *Manager) reconnect(ctx context.Context, ms *managedServer) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Printf("[mcp:%s] attempting reconnect", ms.cfg.Name)
	if ms.session != nil {
		ms.session.Close()
	}

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "nexus",
		Version: "1.0.0",
	}, nil)

	var transport sdkmcp.Transport
	switch ms.cfg.Transport {
	case "stdio":
		parts := buildCommand(ms.cfg.Command, ms.cfg.Args)
		cmd := exec.Command(parts[0], parts[1:]...)
		cmd.Env = os.Environ()
		for k, v := range ms.cfg.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.Stderr = os.Stderr
		transport = &sdkmcp.CommandTransport{Command: cmd}
	case "sse":
		transport = &sdkmcp.SSEClientTransport{Endpoint: ms.cfg.URL}
	}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return err
	}
	ms.session = session

	return m.refreshToolsLocked(ctx, ms)
}

// IsConnected returns true if a server is connected.
func (m *Manager) IsConnected(serverID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.servers[serverID]
	return ok
}

// CloseAll shuts down all server connections.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, ms := range m.servers {
		if ms.session != nil {
			ms.session.Close()
		}
		delete(m.servers, id)
	}
}

// buildCommand splits a command string and appends extra args.
func buildCommand(command string, extraArgs []string) []string {
	parts := strings.Fields(command)
	parts = append(parts, extraArgs...)
	return parts
}

// secondDuration is time.Second as a named constant for timeout contexts.
const secondDuration = 1_000_000_000 // time.Second
