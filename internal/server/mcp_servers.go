package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/mcp"
)

// handleListMCPServers returns all MCP servers for a workspace.
func (s *Server) handleListMCPServers(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	rows, err := wdb.DB.Query(`
		SELECT id, name, transport, command, args, url, env, headers, enabled, tool_prefix, created_at, updated_at
		FROM mcp_servers ORDER BY created_at ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query error")
		return
	}
	defer rows.Close()

	mgr := s.getMCPManager(slug)
	var servers []map[string]any
	for rows.Next() {
		var sid, name, transport, command, argsStr, url, envStr, headersStr, prefix, createdAt, updatedAt string
		var enabled bool
		if err := rows.Scan(&sid, &name, &transport, &command, &argsStr, &url, &envStr, &headersStr, &enabled, &prefix, &createdAt, &updatedAt); err != nil {
			continue
		}

		// Get cached tools from manager
		tools := mgr.ServerTools(sid)
		connected := mgr.IsConnected(sid)

		servers = append(servers, map[string]any{
			"id":          sid,
			"name":        name,
			"transport":   transport,
			"command":     command,
			"args":        json.RawMessage(argsStr),
			"url":         url,
			"env":         maskValues(envStr),
			"headers":     maskValues(headersStr),
			"enabled":     enabled,
			"tool_prefix": prefix,
			"connected":   connected,
			"tool_count":  len(tools),
			"tools":       tools,
			"created_at":  createdAt,
			"updated_at":  updatedAt,
		})
	}

	if servers == nil {
		servers = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, servers)
}

// handleCreateMCPServer creates a new MCP server configuration.
func (s *Server) handleCreateMCPServer(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	var req struct {
		Name      string            `json:"name"`
		Transport string            `json:"transport"`
		Command   string            `json:"command"`
		Args      []string          `json:"args"`
		URL       string            `json:"url"`
		Env       map[string]string `json:"env"`
		Headers   map[string]string `json:"headers"`
		Prefix    string            `json:"tool_prefix"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Transport == "" {
		req.Transport = "stdio"
	}
	if req.Transport != "stdio" && req.Transport != "sse" {
		writeError(w, http.StatusBadRequest, "transport must be 'stdio' or 'sse'")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	serverID := "mcp_" + id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	argsJSON, _ := json.Marshal(req.Args)
	if req.Args == nil {
		argsJSON = []byte("[]")
	}
	envJSON, _ := json.Marshal(req.Env)
	if req.Env == nil {
		envJSON = []byte("{}")
	}
	headersJSON, _ := json.Marshal(req.Headers)
	if req.Headers == nil {
		headersJSON = []byte("{}")
	}

	_, err = wdb.DB.Exec(`
		INSERT INTO mcp_servers (id, name, transport, command, args, url, env, headers, enabled, tool_prefix, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)`,
		serverID, req.Name, req.Transport, req.Command, string(argsJSON), req.URL,
		string(envJSON), string(headersJSON), req.Prefix, now, now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create: "+err.Error())
		return
	}

	// Connect via manager
	cfg := mcp.ServerConfig{
		ID:        serverID,
		Name:      req.Name,
		Transport: req.Transport,
		Command:   req.Command,
		Args:      req.Args,
		URL:       req.URL,
		Env:       req.Env,
		Headers:   req.Headers,
		Prefix:    req.Prefix,
		Enabled:   true,
	}

	mgr := s.getMCPManager(slug)
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	var connectErr string
	if err := mgr.Connect(ctx, cfg); err != nil {
		connectErr = err.Error()
	}

	tools := mgr.ServerTools(serverID)
	connected := mgr.IsConnected(serverID)

	result := map[string]any{
		"id":          serverID,
		"name":        req.Name,
		"transport":   req.Transport,
		"command":     req.Command,
		"args":        req.Args,
		"url":         req.URL,
		"env":         maskMapValues(req.Env),
		"headers":     maskMapValues(req.Headers),
		"enabled":     true,
		"tool_prefix": req.Prefix,
		"connected":   connected,
		"tool_count":  len(tools),
		"tools":       tools,
		"created_at":  now,
		"updated_at":  now,
	}
	if connectErr != "" {
		result["connect_error"] = connectErr
	}

	writeJSON(w, http.StatusCreated, result)
}

// handleGetMCPServer returns a single MCP server by ID.
func (s *Server) handleGetMCPServer(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	serverID := r.PathValue("id")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	var name, transport, command, argsStr, url, envStr, headersStr, prefix, createdAt, updatedAt string
	var enabled bool
	err = wdb.DB.QueryRow(`
		SELECT name, transport, command, args, url, env, headers, enabled, tool_prefix, created_at, updated_at
		FROM mcp_servers WHERE id = ?`, serverID,
	).Scan(&name, &transport, &command, &argsStr, &url, &envStr, &headersStr, &enabled, &prefix, &createdAt, &updatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}

	mgr := s.getMCPManager(slug)
	tools := mgr.ServerTools(serverID)
	connected := mgr.IsConnected(serverID)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":          serverID,
		"name":        name,
		"transport":   transport,
		"command":     command,
		"args":        json.RawMessage(argsStr),
		"url":         url,
		"env":         maskValues(envStr),
		"headers":     maskValues(headersStr),
		"enabled":     enabled,
		"tool_prefix": prefix,
		"connected":   connected,
		"tool_count":  len(tools),
		"tools":       tools,
		"created_at":  createdAt,
		"updated_at":  updatedAt,
	})
}

// handleUpdateMCPServer updates an MCP server configuration.
func (s *Server) handleUpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	serverID := r.PathValue("id")

	var req map[string]any
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Check exists
	var exists int
	wdb.DB.QueryRow("SELECT COUNT(*) FROM mcp_servers WHERE id = ?", serverID).Scan(&exists)
	if exists == 0 {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}

	allowed := map[string]string{
		"name": "name", "transport": "transport", "command": "command",
		"url": "url", "enabled": "enabled", "tool_prefix": "tool_prefix",
	}

	var sets []string
	var args []any
	for key, col := range allowed {
		if val, ok := req[key]; ok {
			sets = append(sets, col+" = ?")
			args = append(args, val)
		}
	}

	// Handle JSON fields
	if v, ok := req["args"]; ok {
		j, _ := json.Marshal(v)
		sets = append(sets, "args = ?")
		args = append(args, string(j))
	}
	if v, ok := req["env"]; ok {
		j, _ := json.Marshal(v)
		sets = append(sets, "env = ?")
		args = append(args, string(j))
	}
	if v, ok := req["headers"]; ok {
		j, _ := json.Marshal(v)
		sets = append(sets, "headers = ?")
		args = append(args, string(j))
	}

	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "no valid fields to update")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sets = append(sets, "updated_at = ?")
	args = append(args, now)
	args = append(args, serverID)

	_, err = wdb.DB.Exec("UPDATE mcp_servers SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed: "+err.Error())
		return
	}

	// Reconnect with updated config
	mgr := s.getMCPManager(slug)
	cfg := s.loadMCPServerConfig(slug, serverID)
	if cfg != nil && cfg.Enabled {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		mgr.Connect(ctx, *cfg)
	} else if cfg != nil && !cfg.Enabled {
		mgr.Disconnect(serverID)
	}

	// Return updated server
	s.handleGetMCPServer(w, r)
}

// handleDeleteMCPServer deletes an MCP server.
func (s *Server) handleDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	serverID := r.PathValue("id")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Disconnect
	mgr := s.getMCPManager(slug)
	mgr.Disconnect(serverID)

	_, _ = wdb.DB.Exec("DELETE FROM mcp_servers WHERE id = ?", serverID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleRefreshMCPServer re-discovers tools from a connected server.
func (s *Server) handleRefreshMCPServer(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	serverID := r.PathValue("id")

	mgr := s.getMCPManager(slug)

	if !mgr.IsConnected(serverID) {
		// Try to connect first
		cfg := s.loadMCPServerConfig(slug, serverID)
		if cfg == nil {
			writeError(w, http.StatusNotFound, "server not found")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := mgr.Connect(ctx, *cfg); err != nil {
			writeError(w, http.StatusInternalServerError, "connect failed: "+err.Error())
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	tools, err := mgr.RefreshTools(ctx, serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "refresh failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tools":      tools,
		"tool_count": len(tools),
	})
}

// loadMCPServerConfig reads a server config from the database.
func (s *Server) loadMCPServerConfig(slug, serverID string) *mcp.ServerConfig {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return nil
	}

	var name, transport, command, argsStr, url, envStr, headersStr, prefix string
	var enabled bool
	err = wdb.DB.QueryRow(`
		SELECT name, transport, command, args, url, env, headers, enabled, tool_prefix
		FROM mcp_servers WHERE id = ?`, serverID,
	).Scan(&name, &transport, &command, &argsStr, &url, &envStr, &headersStr, &enabled, &prefix)
	if err != nil {
		return nil
	}

	var args []string
	json.Unmarshal([]byte(argsStr), &args)
	var env map[string]string
	json.Unmarshal([]byte(envStr), &env)
	var headers map[string]string
	json.Unmarshal([]byte(headersStr), &headers)

	return &mcp.ServerConfig{
		ID:        serverID,
		Name:      name,
		Transport: transport,
		Command:   command,
		Args:      args,
		URL:       url,
		Env:       env,
		Headers:   headers,
		Prefix:    prefix,
		Enabled:   enabled,
	}
}

// maskValues takes a JSON object string and replaces values with "***".
func maskValues(jsonStr string) json.RawMessage {
	var m map[string]string
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return json.RawMessage(jsonStr)
	}
	masked := make(map[string]string, len(m))
	for k := range m {
		masked[k] = "***"
	}
	b, _ := json.Marshal(masked)
	return b
}

// maskMapValues masks values in a map directly.
func maskMapValues(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	masked := make(map[string]string, len(m))
	for k := range m {
		masked[k] = "***"
	}
	return masked
}
