package brain

import "encoding/json"

// Tool definitions for OpenRouter function calling (OpenAI-compatible format).

type ToolDef struct {
	Type     string      `json:"type"`
	Function ToolFuncDef `json:"function"`
}

type ToolFuncDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ToolMessage represents a tool result message in the conversation.
type ToolMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ToolCompletionChoice extends CompletionChoice with tool calls.
type ToolCompletionChoice struct {
	Message struct {
		Role      string         `json:"role"`
		Content   string         `json:"content"`
		ToolCalls []ToolCall     `json:"tool_calls,omitempty"`
		Images    []MessageImage `json:"images,omitempty"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
}

type ToolCompletionResponse struct {
	Choices []ToolCompletionChoice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Available tools for the Brain.
var Tools = []ToolDef{
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "create_task",
			Description: "Create a new task in the workspace. Use this when someone asks you to track, assign, or create a task/todo.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Task title"},
					"description": {"type": "string", "description": "Task description (optional)"},
					"status": {"type": "string", "enum": ["backlog", "todo", "in_progress", "done"], "description": "Initial status (default: todo)"},
					"priority": {"type": "string", "enum": ["low", "medium", "high", "urgent"], "description": "Priority level (default: medium)"},
					"assignee_name": {"type": "string", "description": "Display name of person to assign to (optional)"}
				},
				"required": ["title"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "list_tasks",
			Description: "List tasks in the workspace. Use this when someone asks about tasks, what's in progress, what's overdue, etc.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"status": {"type": "string", "enum": ["backlog", "todo", "in_progress", "done", "cancelled"], "description": "Filter by status (optional)"},
					"assignee_name": {"type": "string", "description": "Filter by assignee display name (optional)"}
				}
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "search_messages",
			Description: "Search for messages in the current channel or workspace. Use this when someone asks about past conversations or what was said about a topic.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query (matched against message content)"},
					"channel_id": {"type": "string", "description": "Limit search to specific channel (optional, defaults to current channel)"},
					"limit": {"type": "integer", "description": "Max results (default: 10)"}
				},
				"required": ["query"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "create_document",
			Description: "Create a new document/note. Use when someone asks you to write up, document, or create a note about something.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Document title"},
					"content": {"type": "string", "description": "Document content (markdown)"}
				},
				"required": ["title", "content"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "search_knowledge",
			Description: "Search the workspace knowledge base for reference materials and documentation. Use when someone asks about topics that may be covered in uploaded knowledge articles.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query to find relevant knowledge articles"}
				},
				"required": ["query"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "delegate_to_agent",
			Description: "Delegate a task to a specialized AI agent in the workspace. Use when a request matches another agent's expertise.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"agent_name": {"type": "string", "description": "The name of the agent to delegate to"},
					"task": {"type": "string", "description": "The task or question to delegate"}
				},
				"required": ["agent_name", "task"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "generate_image",
			Description: "Generate an image from a text prompt. Use when the user asks you to create, generate, draw, or design an image, illustration, logo, ad visual, or any graphic.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"prompt": {"type": "string", "description": "Detailed image description. Be specific about subject, style, composition, colors, lighting, and mood."}
				},
				"required": ["prompt"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "send_email",
			Description: "Send an email. Use for replying to emails, sending notifications, or reaching out to contacts.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"to": {"type": "string", "description": "Recipient email address"},
					"subject": {"type": "string", "description": "Email subject line"},
					"body": {"type": "string", "description": "Email body (plain text)"}
				},
				"required": ["to", "subject", "body"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "send_telegram",
			Description: "Send a message to a Telegram chat. The chat is determined from the current channel's linked Telegram integration.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"message": {"type": "string", "description": "Message text to send"}
				},
				"required": ["message"]
			}`),
		},
	},
}
