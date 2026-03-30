package hub

import "encoding/json"

// Client → Server message types
const (
	TypeMessageSend   = "message.send"
	TypeMessageEdit   = "message.edit"
	TypeMessageDelete = "message.delete"
	TypeReactionAdd   = "reaction.add"
	TypeReactionRemove = "reaction.remove"
	TypeTypingStart   = "typing.start"
	TypeTypingStop    = "typing.stop"
	TypeChannelJoin   = "channel.join"
	TypeChannelLeave  = "channel.leave"
	TypeChannelClear   = "channel.clear"
	TypeChannelRead    = "channel.read"
	TypePresenceUpdate = "presence.update"
)

// Server → Client message types
const (
	TypeMessageNew     = "message.new"
	TypeMessageEdited  = "message.edited"
	TypeMessageDeleted = "message.deleted"
	TypeReactionAdded  = "reaction.added"
	TypeReactionRemoved = "reaction.removed"
	TypeTyping         = "typing"
	TypePresence       = "presence"
	TypeChannelJoined  = "channel.joined"
	TypeChannelCleared  = "channel.cleared"
	TypeChannelArchived = "channel.archived"
	TypeError          = "error"
	TypeTaskCreated    = "task.created"
	TypeTaskUpdated    = "task.updated"
	TypeTaskDeleted    = "task.deleted"
	TypeEventCreated   = "event.created"
	TypeEventUpdated   = "event.updated"
	TypeEventDeleted   = "event.deleted"
	TypeEventReminder  = "event.reminder"
	TypeAgentState          = "agent.state"
	TypeUnreadUpdate       = "unread.update"
	TypeChannelMemberRemoved = "channel.member_removed"
	TypeChannelMemberAdded   = "channel.member_added"
	TypeActivityNew          = "activity.new"
	TypeSocialPulseCreated = "social_pulse.created"
	TypeSocialPulseUpdated = "social_pulse.updated"
	TypeSocialPulseDeleted = "social_pulse.deleted"
	TypeBridgeStatus       = "bridge.status"
	TypeNotification       = "notification.new"
)

// Payload types for messages
type MessageSendPayload struct {
	ChannelID string `json:"channel_id"`
	Content   string `json:"content"`
	ClientID  string `json:"client_id,omitempty"`
	ParentID  string `json:"parent_id,omitempty"`
	WebLLM    bool   `json:"webllm,omitempty"`
}

type MessageEditPayload struct {
	MessageID string `json:"message_id"`
	ChannelID string `json:"channel_id"`
	Content   string `json:"content"`
}

type MessageDeletePayload struct {
	MessageID string `json:"message_id"`
	ChannelID string `json:"channel_id"`
}

type MessageNewPayload struct {
	ID            string   `json:"id"`
	ChannelID     string   `json:"channel_id"`
	SenderID      string   `json:"sender_id"`
	SenderName    string   `json:"sender_name"`
	Content       string   `json:"content"`
	CreatedAt     string   `json:"created_at"`
	ToolsUsed     []string `json:"tools_used,omitempty"`
	ClientID      string   `json:"client_id,omitempty"`
	ParentID      string   `json:"parent_id,omitempty"`
	ReplyCount    int      `json:"reply_count,omitempty"`
	LatestReplyAt string   `json:"latest_reply_at,omitempty"`
}

type MessageEditedPayload struct {
	MessageID string `json:"message_id"`
	ChannelID string `json:"channel_id"`
	Content   string `json:"content"`
	EditedAt  string `json:"edited_at"`
}

type MessageDeletedPayload struct {
	MessageID string `json:"message_id"`
	ChannelID string `json:"channel_id"`
}

type ReactionPayload struct {
	MessageID string `json:"message_id"`
	ChannelID string `json:"channel_id"`
	Emoji     string `json:"emoji"`
	UserID    string `json:"user_id,omitempty"`
	UserName  string `json:"user_name,omitempty"`
}

type TypingPayload struct {
	ChannelID   string `json:"channel_id"`
	UserID      string `json:"user_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type PresencePayload struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"` // online, offline
}

type ChannelJoinPayload struct {
	ChannelID string `json:"channel_id"`
}

type ChannelClearPayload struct {
	ChannelID string `json:"channel_id"`
}

type ChannelClearedPayload struct {
	ChannelID string `json:"channel_id"`
}

type ChannelReadPayload struct {
	ChannelID string `json:"channel_id"`
}

type UnreadUpdatePayload struct {
	ChannelID string `json:"channel_id"`
	Unread    int    `json:"unread"`
}

type NotificationPayload struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Link      string `json:"link"`
	ActorID   string `json:"actor_id"`
	ActorName string `json:"actor_name"`
	SourceID  string `json:"source_id"`
	CreatedAt string `json:"created_at"`
}

type ErrorPayload struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// Task payloads (server → client broadcasts)
type TaskPayload struct {
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	Description    string          `json:"description,omitempty"`
	ExpectedOutput string          `json:"expected_output,omitempty"`
	Status         string          `json:"status"`
	Priority       string          `json:"priority"`
	AssigneeID     string          `json:"assignee_id,omitempty"`
	CreatedBy      string          `json:"created_by"`
	DueDate        string          `json:"due_date,omitempty"`
	Tags           json.RawMessage `json:"tags"`
	ChannelID      string          `json:"channel_id,omitempty"`
	MessageID      string          `json:"message_id,omitempty"`
	Position       int             `json:"position"`
	ScheduledAt    string          `json:"scheduled_at,omitempty"`
	AgentID        string          `json:"agent_id,omitempty"`
	RecurrenceRule string          `json:"recurrence_rule,omitempty"`
	RecurrenceEnd  string          `json:"recurrence_end,omitempty"`
	RunCount       int             `json:"run_count"`
	LastRunAt      string          `json:"last_run_at,omitempty"`
	LastRunStatus  string          `json:"last_run_status,omitempty"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
}

type TaskDeletedPayload struct {
	ID string `json:"id"`
}

// AgentStatePayload broadcasts agent lifecycle state to clients.
type AgentStatePayload struct {
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	ChannelID string `json:"channel_id"`
	ParentID  string `json:"parent_id,omitempty"`
	State     string `json:"state"`               // "thinking", "tool_executing", "idle"
	ToolName  string `json:"tool_name,omitempty"`
}

// Calendar event payloads (server → client broadcasts)
type EventPayload struct {
	ID                string          `json:"id"`
	Title             string          `json:"title"`
	Description       string          `json:"description,omitempty"`
	Location          string          `json:"location,omitempty"`
	StartTime         string          `json:"start_time"`
	EndTime           string          `json:"end_time"`
	AllDay            bool            `json:"all_day"`
	RecurrenceRule    string          `json:"recurrence_rule,omitempty"`
	RecurrenceParentID string         `json:"recurrence_parent_id,omitempty"`
	Color             string          `json:"color,omitempty"`
	DisplayColor      string          `json:"display_color,omitempty"`
	Calendar          string          `json:"calendar"`
	CreatedBy         string          `json:"created_by"`
	CreatedByName     string          `json:"created_by_name,omitempty"`
	AgentID           string          `json:"agent_id,omitempty"`
	Model             string          `json:"model,omitempty"`
	Attendees         json.RawMessage `json:"attendees"`
	Reminders         json.RawMessage `json:"reminders"`
	ChannelID         string          `json:"channel_id,omitempty"`
	Status            string          `json:"status"`
	ExecutedAt        string          `json:"executed_at,omitempty"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
}

type EventDeletedPayload struct {
	ID string `json:"id"`
}

type EventReminderPayload struct {
	EventID   string `json:"event_id"`
	Title     string `json:"title"`
	StartTime string `json:"start_time"`
	Minutes   int    `json:"minutes_before"`
}

type ChannelMemberRemovedPayload struct {
	ChannelID string `json:"channel_id"`
	MemberID  string `json:"member_id"`
}

type ChannelMemberAddedPayload struct {
	ChannelID  string `json:"channel_id"`
	MemberID   string `json:"member_id"`
	MemberName string `json:"member_name"`
	Role       string `json:"role"`
}

// MakeEnvelope creates a JSON-encoded envelope.
func MakeEnvelope(msgType string, payload any) []byte {
	p, _ := json.Marshal(payload)
	env := Envelope{Type: msgType, Payload: p}
	data, _ := json.Marshal(env)
	return data
}
