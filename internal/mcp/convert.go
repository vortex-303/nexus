package mcp

import (
	"encoding/json"
	"strings"

	"github.com/nexus-chat/nexus/internal/brain"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToToolDef converts an MCP ToolInfo to a brain.ToolDef (OpenAI function calling format).
func ToToolDef(info ToolInfo) brain.ToolDef {
	params := info.InputSchema
	if len(params) == 0 {
		params = json.RawMessage(`{"type":"object"}`)
	}
	return brain.ToolDef{
		Type: "function",
		Function: brain.ToolFuncDef{
			Name:        info.QualName,
			Description: info.Description,
			Parameters:  params,
		},
	}
}

// ToToolDefs converts a slice of ToolInfo to brain.ToolDef slice.
func ToToolDefs(infos []ToolInfo) []brain.ToolDef {
	defs := make([]brain.ToolDef, len(infos))
	for i, info := range infos {
		defs[i] = ToToolDef(info)
	}
	return defs
}

// ContentToString extracts text from an MCP CallToolResult.
func ContentToString(result *sdkmcp.CallToolResult) string {
	if result == nil {
		return ""
	}

	var parts []string
	for _, c := range result.Content {
		if text, ok := c.(*sdkmcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}

	if len(parts) == 0 {
		// Fallback: try to marshal the whole result
		b, err := json.Marshal(result.Content)
		if err == nil {
			return string(b)
		}
		return ""
	}

	return strings.Join(parts, "\n")
}
