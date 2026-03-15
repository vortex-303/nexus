package brain

import "encoding/json"

// APIError handles both object {"message":"..."} and plain string error responses.
type APIError struct {
	Message string
}

func (e *APIError) UnmarshalJSON(data []byte) error {
	// Try object first: {"message": "..."}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		e.Message = obj.Message
		return nil
	}
	// Fall back to plain string: "error text"
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		e.Message = s
		return nil
	}
	e.Message = string(data)
	return nil
}

// Tool definitions for OpenRouter function calling (OpenAI-compatible format).

type ToolDef struct {
	Type     string      `json:"type"`
	Function ToolFuncDef `json:"function"`
}

type ToolFuncDef struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Parameters     json.RawMessage `json:"parameters"`
	ResultAsAnswer bool            `json:"-"` // Not sent to LLM, internal only
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	ThoughtSignature string `json:"-"` // Gemini thought_signature, not sent via OpenRouter
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
	Usage   *CompletionUsage       `json:"usage,omitempty"`
	Error   *APIError              `json:"error,omitempty"`
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
					"expected_output": {"type": "string", "description": "What 'done' looks like — the acceptance criteria"},
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
			Name:        "update_task",
			Description: "Update an existing task. Use when someone asks to mark a task done, change its status, priority, assignee, or due date.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"task_id": {"type": "string", "description": "The task ID to update"},
					"title": {"type": "string", "description": "New title"},
					"status": {"type": "string", "enum": ["backlog", "todo", "in_progress", "done", "cancelled"], "description": "New status"},
					"priority": {"type": "string", "enum": ["low", "medium", "high", "urgent"], "description": "New priority"},
					"assignee_name": {"type": "string", "description": "Display name of new assignee (empty string to unassign)"},
					"due_date": {"type": "string", "description": "Due date in ISO 8601 (empty string to clear)"},
					"description": {"type": "string", "description": "New description"}
				},
				"required": ["task_id"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "delete_task",
			Description: "Delete a task. Use when someone asks to remove or delete a task.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"task_id": {"type": "string", "description": "The task ID to delete"}
				},
				"required": ["task_id"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "search_messages",
			Description: "Search past messages across workspace channels. Returns who said what, when, and in which channel. Use to recall conversations, find decisions, or look up what someone said.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search keywords"},
					"channel_name": {"type": "string", "description": "Filter to specific channel by name (optional)"},
					"sender_name": {"type": "string", "description": "Filter by sender display name (optional)"},
					"limit": {"type": "integer", "description": "Max results (default 10, max 20)"}
				},
				"required": ["query"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:           "create_document",
			Description:    "Create a new document/note. Use when someone asks you to write up, document, or create a note about something.",
			ResultAsAnswer: true,
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
			Description: "Search across knowledge articles, documents, and uploaded files for reference materials. Use when someone asks about topics that may be covered in workspace content.",
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
			Description: "Delegate a task to a specialized agent. The agent will use its tools and skills to complete the work.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"agent_name": {"type": "string", "description": "The name of the agent to delegate to"},
					"task": {"type": "string", "description": "What to do"},
					"context": {"type": "string", "description": "Background info, prior findings, relevant data"},
					"expected_output": {"type": "string", "description": "What the result should look like"}
				},
				"required": ["agent_name", "task"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "ask_agent",
			Description: "Ask a specific agent a question and get a direct answer. Use for quick queries, not full tasks.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"agent_name": {"type": "string", "description": "The name of the agent to ask"},
					"question": {"type": "string", "description": "The question to ask"}
				},
				"required": ["agent_name", "question"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "recall_memory",
			Description: "Search workspace memory for specific facts, decisions, or commitments. Use when you need to remember something discussed before.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "What to search for"},
					"type": {"type": "string", "enum": ["fact", "decision", "commitment", "person"], "description": "Filter by memory type (optional)"}
				},
				"required": ["query"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "save_memory",
			Description: "Save an important fact, decision, or commitment to workspace memory for future reference.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"content": {"type": "string", "description": "The memory to save"},
					"type": {"type": "string", "enum": ["fact", "decision", "commitment", "person"], "description": "Memory type (default: fact)"},
					"importance": {"type": "number", "description": "0.0 to 1.0 (default: 0.5)"}
				},
				"required": ["content"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:           "generate_image",
			Description:    "Generate an image from a text prompt. Use when the user asks you to create, generate, draw, or design an image, illustration, logo, ad visual, or any graphic.",
			ResultAsAnswer: true,
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
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "create_calendar_event",
			Description: "Create a calendar event. Use when someone asks to schedule a meeting, appointment, deadline, or any time-based event.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Event title"},
					"start_time": {"type": "string", "description": "Start time in ISO 8601 format (e.g. 2025-03-15T14:00:00Z)"},
					"end_time": {"type": "string", "description": "End time in ISO 8601 format"},
					"description": {"type": "string", "description": "Event description (optional)"},
					"location": {"type": "string", "description": "Event location (optional)"},
					"all_day": {"type": "boolean", "description": "Whether this is an all-day event (default: false)"},
					"recurrence_rule": {"type": "string", "description": "RRULE string for recurring events (e.g. FREQ=WEEKLY;BYDAY=MO,WE,FR)"},
					"attendee_names": {"type": "array", "items": {"type": "string"}, "description": "Display names of attendees (optional)"},
					"reminders": {"type": "array", "items": {"type": "object", "properties": {"minutes_before": {"type": "integer"}, "type": {"type": "string", "enum": ["notification", "email"]}}}, "description": "Reminders (optional)"}
				},
				"required": ["title", "start_time", "end_time"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "list_calendar_events",
			Description: "List upcoming calendar events. Use when someone asks about their schedule, what's coming up, or what meetings are planned.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"start": {"type": "string", "description": "Range start in ISO 8601 (default: now)"},
					"end": {"type": "string", "description": "Range end in ISO 8601 (default: 7 days from now)"},
					"calendar": {"type": "string", "description": "Filter by calendar name (optional)"}
				}
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "update_calendar_event",
			Description: "Update an existing calendar event. Use when someone asks to reschedule, rename, or modify an event.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"event_id": {"type": "string", "description": "The event ID to update"},
					"title": {"type": "string", "description": "New title"},
					"start_time": {"type": "string", "description": "New start time (ISO 8601)"},
					"end_time": {"type": "string", "description": "New end time (ISO 8601)"},
					"description": {"type": "string", "description": "New description"},
					"location": {"type": "string", "description": "New location"},
					"status": {"type": "string", "enum": ["confirmed", "tentative", "cancelled"], "description": "Event status"}
				},
				"required": ["event_id"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "delete_calendar_event",
			Description: "Delete a calendar event. Use when someone asks to cancel or remove an event.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"event_id": {"type": "string", "description": "The event ID to delete"}
				},
				"required": ["event_id"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:           "web_search",
			Description:    "Search the INTERNET/WEB for real-time information. Use this whenever someone asks to search the web, look something up online, find current prices, news, or any information not in the workspace. This searches the public internet, NOT workspace messages.",
			ResultAsAnswer: true,
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"num_results": {"type": "integer", "description": "Number of results to return (default 5, max 10)"}
				},
				"required": ["query"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:           "search_x",
			Description:    "Search X/Twitter for posts, discussions, and real-time social media content. Use when someone asks what people are saying on X/Twitter, wants social media sentiment, or asks about trending discussions.",
			ResultAsAnswer: true,
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query for X/Twitter"},
					"from_date": {"type": "string", "description": "Only include posts from this date (YYYY-MM-DD, optional)"},
					"to_date": {"type": "string", "description": "Only include posts up to this date (YYYY-MM-DD, optional)"},
					"x_handles": {"type": "array", "items": {"type": "string"}, "description": "Only search posts from these X handles (optional)"}
				},
				"required": ["query"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:           "fetch_url",
			Description:    "Fetch a web page and extract its text content. Use this to read articles, documentation, or any public URL.",
			ResultAsAnswer: true,
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "The URL to fetch (must be http or https)"}
				},
				"required": ["url"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "trace_knowledge",
			Description: "Look up the source and provenance of something Brain knows. Use when asked where information came from, to cite sources, or to verify claims.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "The claim or fact to trace"}
				},
				"required": ["query"]
			}`),
		},
	},
}
