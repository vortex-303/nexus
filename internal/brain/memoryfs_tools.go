package brain

import "encoding/json"

// MemoryFSTools are the five file tools over Brain's persistent memory
// directory (the v4 port of v3's memory_store file tools). Appended to the
// shared Tools catalog at init.
var MemoryFSTools = []ToolDef{
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "read_memory",
			Description: "Read a file from your persistent memory (e.g. /pinned.md, /people/alice.md). Use before answering questions about people, decisions, or project context you may have stored.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Memory file path, e.g. /people/alice.md"}
				},
				"required": ["path"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "write_memory",
			Description: "Create or replace a file in your persistent memory. Use for new decisions (/decisions/YYYY-MM-DD-topic.md), new people profiles, project notes. Prefer edit_memory for updating existing files.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Memory file path, e.g. /decisions/2026-07-27-pricing.md"},
					"content": {"type": "string", "description": "Full markdown content of the file"}
				},
				"required": ["path", "content"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "edit_memory",
			Description: "Edit a memory file in place by replacing an exact text snippet. Use when correcting or extending stored facts instead of rewriting the whole file.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Memory file path"},
					"old_string": {"type": "string", "description": "Exact text to replace (must exist in the file)"},
					"new_string": {"type": "string", "description": "Replacement text"}
				},
				"required": ["path", "old_string", "new_string"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "glob_memory",
			Description: "List memory files matching a glob pattern (e.g. /people/*.md, decisions/2026-*). Use to discover what you have stored.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"pattern": {"type": "string", "description": "Glob pattern; empty lists everything"}
				}
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "grep_memory",
			Description: "Search the contents of all memory files for a text query (case-insensitive). Returns matching lines with their file paths.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Text to search for"}
				},
				"required": ["query"]
			}`),
		},
	},
}

func init() {
	Tools = append(Tools, MemoryFSTools...)
}
