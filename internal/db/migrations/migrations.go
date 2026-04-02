package migrations

import (
	"database/sql"
	"fmt"
	"os"
)

type migration struct {
	version int
	name    string
	sql     string
}

var globalMigrations = []migration{
	{
		version: 1,
		name:    "initial schema",
		sql: `
			CREATE TABLE IF NOT EXISTS accounts (
				id TEXT PRIMARY KEY,
				email TEXT UNIQUE,
				password_hash TEXT,
				display_name TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE TABLE IF NOT EXISTS workspaces (
				slug TEXT PRIMARY KEY,
				name TEXT NOT NULL DEFAULT '',
				created_by TEXT,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE TABLE IF NOT EXISTS sessions (
				token TEXT PRIMARY KEY,
				account_id TEXT,
				display_name TEXT NOT NULL,
				workspace_slug TEXT,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				expires_at TEXT NOT NULL
			);

			CREATE TABLE IF NOT EXISTS invite_tokens (
				token TEXT PRIMARY KEY,
				workspace_slug TEXT NOT NULL REFERENCES workspaces(slug),
				created_by TEXT NOT NULL,
				expires_at TEXT,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE TABLE IF NOT EXISTS jwt_secrets (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				secret TEXT NOT NULL
			);
		`,
	},
	{
		version: 2,
		name:    "superadmin and platform management",
		sql: `
			ALTER TABLE accounts ADD COLUMN is_superadmin BOOLEAN NOT NULL DEFAULT FALSE;
			ALTER TABLE accounts ADD COLUMN banned BOOLEAN NOT NULL DEFAULT FALSE;

			ALTER TABLE workspaces ADD COLUMN suspended BOOLEAN NOT NULL DEFAULT FALSE;
			ALTER TABLE workspaces ADD COLUMN suspended_reason TEXT NOT NULL DEFAULT '';

			CREATE TABLE IF NOT EXISTS admin_audit_log (
				id TEXT PRIMARY KEY,
				actor_id TEXT NOT NULL,
				actor_email TEXT NOT NULL,
				action TEXT NOT NULL,
				target_type TEXT NOT NULL,
				target_id TEXT NOT NULL,
				detail TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE INDEX IF NOT EXISTS idx_admin_audit_created ON admin_audit_log(created_at);
		`,
	},
	{
		version: 3,
		name:    "platform announcements and models",
		sql: `
			CREATE TABLE IF NOT EXISTS platform_announcements (
				id TEXT PRIMARY KEY,
				message TEXT NOT NULL,
				type TEXT NOT NULL DEFAULT 'info',
				active BOOLEAN NOT NULL DEFAULT TRUE,
				created_by TEXT NOT NULL,
				created_at TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE TABLE IF NOT EXISTS platform_models (
				id TEXT PRIMARY KEY,
				display_name TEXT NOT NULL,
				provider TEXT NOT NULL,
				context_length INTEGER,
				supports_tools BOOLEAN DEFAULT FALSE,
				pinned_at TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				pinned_by TEXT NOT NULL
			);
		`,
	},
	{
		version: 4,
		name:    "email_verifications",
		sql: `
			CREATE TABLE IF NOT EXISTS email_verifications (
				id TEXT PRIMARY KEY,
				email TEXT NOT NULL,
				code TEXT NOT NULL,
				expires_at TEXT NOT NULL,
				verified BOOLEAN NOT NULL DEFAULT FALSE,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);
			CREATE INDEX IF NOT EXISTS idx_email_verifications_email ON email_verifications(email, verified);
		`,
	},
	{
		version: 5,
		name:    "password_resets and email_verified",
		sql: `
			CREATE TABLE IF NOT EXISTS password_resets (
				id TEXT PRIMARY KEY,
				email TEXT NOT NULL,
				token TEXT NOT NULL UNIQUE,
				expires_at TEXT NOT NULL,
				used BOOLEAN NOT NULL DEFAULT FALSE,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
			);
			CREATE INDEX idx_password_resets_token ON password_resets(token);
			ALTER TABLE accounts ADD COLUMN email_verified BOOLEAN NOT NULL DEFAULT TRUE;
		`,
	},
	{
		version: 6,
		name:    "workspace member limits and waitlist",
		sql: `
			ALTER TABLE workspaces ADD COLUMN max_members INTEGER NOT NULL DEFAULT 5;
			CREATE TABLE IF NOT EXISTS waitlist (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				email TEXT UNIQUE NOT NULL,
				plan TEXT NOT NULL DEFAULT 'pro',
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);
		`,
	},
}

var workspaceMigrations = []migration{
	{
		version: 1,
		name:    "initial schema",
		sql: `
			CREATE TABLE IF NOT EXISTS members (
				id TEXT PRIMARY KEY,
				account_id TEXT,
				display_name TEXT NOT NULL,
				role TEXT NOT NULL DEFAULT 'member',
				joined_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE TABLE IF NOT EXISTS channels (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				type TEXT NOT NULL DEFAULT 'public',
				classification TEXT NOT NULL DEFAULT 'public',
				created_by TEXT,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				archived BOOLEAN NOT NULL DEFAULT FALSE
			);

			CREATE TABLE IF NOT EXISTS messages (
				id TEXT PRIMARY KEY,
				channel_id TEXT NOT NULL REFERENCES channels(id),
				sender_id TEXT NOT NULL,
				content TEXT NOT NULL,
				encrypted_payload BLOB,
				edited_at TEXT,
				deleted BOOLEAN NOT NULL DEFAULT FALSE,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE INDEX IF NOT EXISTS idx_messages_channel ON messages(channel_id, created_at);

			CREATE TABLE IF NOT EXISTS reactions (
				message_id TEXT NOT NULL REFERENCES messages(id),
				user_id TEXT NOT NULL,
				emoji TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				PRIMARY KEY (message_id, user_id, emoji)
			);

			CREATE TABLE IF NOT EXISTS channel_reads (
				channel_id TEXT NOT NULL REFERENCES channels(id),
				user_id TEXT NOT NULL,
				last_read_at TEXT NOT NULL,
				PRIMARY KEY (channel_id, user_id)
			);

			CREATE TABLE IF NOT EXISTS permission_overrides (
				member_id TEXT NOT NULL REFERENCES members(id),
				permission TEXT NOT NULL,
				granted BOOLEAN NOT NULL,
				PRIMARY KEY (member_id, permission)
			);

			CREATE TABLE IF NOT EXISTS guest_channels (
				member_id TEXT NOT NULL REFERENCES members(id),
				channel_id TEXT NOT NULL REFERENCES channels(id),
				PRIMARY KEY (member_id, channel_id)
			);
		`,
	},
	{
		version: 2,
		name:    "tasks",
		sql: `
			CREATE TABLE IF NOT EXISTS tasks (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'backlog',
				priority TEXT NOT NULL DEFAULT 'medium',
				assignee_id TEXT,
				created_by TEXT NOT NULL,
				due_date TEXT,
				tags TEXT NOT NULL DEFAULT '[]',
				channel_id TEXT,
				message_id TEXT,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
			CREATE INDEX IF NOT EXISTS idx_tasks_assignee ON tasks(assignee_id);
		`,
	},
	{
		version: 3,
		name:    "files",
		sql: `
			CREATE TABLE IF NOT EXISTS files (
				id TEXT PRIMARY KEY,
				channel_id TEXT NOT NULL,
				uploader_id TEXT NOT NULL,
				name TEXT NOT NULL,
				mime TEXT NOT NULL DEFAULT 'application/octet-stream',
				size INTEGER NOT NULL DEFAULT 0,
				hash TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE INDEX IF NOT EXISTS idx_files_channel ON files(channel_id, created_at);
			CREATE INDEX IF NOT EXISTS idx_files_hash ON files(hash);
		`,
	},
	{
		version: 4,
		name:    "documents",
		sql: `
			CREATE TABLE IF NOT EXISTS documents (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL DEFAULT '',
				content TEXT NOT NULL DEFAULT '',
				created_by TEXT NOT NULL,
				updated_by TEXT,
				sharing TEXT NOT NULL DEFAULT 'workspace',
				channel_id TEXT,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE INDEX IF NOT EXISTS idx_documents_created ON documents(created_at);
		`,
	},
	{
		version: 5,
		name:    "brain_settings",
		sql: `
			CREATE TABLE IF NOT EXISTS brain_settings (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL DEFAULT ''
			);
		`,
	},
	{
		version: 6,
		name:    "brain_action_log",
		sql: `
			CREATE TABLE IF NOT EXISTS brain_action_log (
				id TEXT PRIMARY KEY,
				action_type TEXT NOT NULL,
				channel_id TEXT,
				trigger_text TEXT NOT NULL DEFAULT '',
				response_text TEXT NOT NULL DEFAULT '',
				tools_used TEXT NOT NULL DEFAULT '[]',
				model TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE INDEX IF NOT EXISTS idx_brain_action_log_created ON brain_action_log(created_at);
		`,
	},
	{
		version: 7,
		name:    "brain_memories",
		sql: `
			CREATE TABLE IF NOT EXISTS brain_memories (
				id TEXT PRIMARY KEY,
				type TEXT NOT NULL DEFAULT 'fact',
				content TEXT NOT NULL,
				source_channel TEXT,
				source_message_id TEXT,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE INDEX IF NOT EXISTS idx_brain_memories_type ON brain_memories(type);
			CREATE INDEX IF NOT EXISTS idx_brain_memories_created ON brain_memories(created_at);

			CREATE TABLE IF NOT EXISTS brain_channel_summaries (
				channel_id TEXT PRIMARY KEY,
				summary TEXT NOT NULL DEFAULT '',
				message_count INTEGER NOT NULL DEFAULT 0,
				last_message_id TEXT,
				updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);
		`,
	},
	{
		version: 8,
		name:    "brain_knowledge",
		sql: `
			CREATE TABLE IF NOT EXISTS brain_knowledge (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL,
				content TEXT NOT NULL,
				source_type TEXT NOT NULL DEFAULT 'text',
				source_name TEXT,
				tokens INTEGER DEFAULT 0,
				created_by TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE INDEX IF NOT EXISTS idx_brain_knowledge_created ON brain_knowledge(created_at);
		`,
	},
	{
		version: 9,
		name:    "agents",
		sql: `
			CREATE TABLE IF NOT EXISTS agents (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				avatar TEXT NOT NULL DEFAULT '',

				-- Personality
				role TEXT NOT NULL DEFAULT '',
				goal TEXT NOT NULL DEFAULT '',
				backstory TEXT NOT NULL DEFAULT '',
				instructions TEXT NOT NULL DEFAULT '',

				-- LLM Config
				model TEXT NOT NULL DEFAULT '',
				temperature REAL NOT NULL DEFAULT 0.7,
				max_tokens INTEGER NOT NULL DEFAULT 2048,

				-- Capabilities
				tools TEXT NOT NULL DEFAULT '[]',
				channels TEXT NOT NULL DEFAULT '[]',
				knowledge_access BOOLEAN NOT NULL DEFAULT FALSE,
				memory_access BOOLEAN NOT NULL DEFAULT FALSE,
				can_delegate BOOLEAN NOT NULL DEFAULT FALSE,

				-- Guardrails
				max_iterations INTEGER NOT NULL DEFAULT 5,
				requires_approval TEXT NOT NULL DEFAULT '[]',
				constraints TEXT NOT NULL DEFAULT '',
				escalation_prompt TEXT NOT NULL DEFAULT '',

				-- Triggers
				trigger_type TEXT NOT NULL DEFAULT 'mention',
				trigger_config TEXT NOT NULL DEFAULT '',

				-- Status
				is_system BOOLEAN NOT NULL DEFAULT FALSE,
				is_active BOOLEAN NOT NULL DEFAULT TRUE,

				-- Metadata
				template_id TEXT,
				created_by TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE INDEX IF NOT EXISTS idx_agents_active ON agents(is_active);

			ALTER TABLE members ADD COLUMN reports_to TEXT NOT NULL DEFAULT '';
			ALTER TABLE members ADD COLUMN title TEXT NOT NULL DEFAULT '';
			ALTER TABLE members ADD COLUMN bio TEXT NOT NULL DEFAULT '';
			ALTER TABLE members ADD COLUMN goals TEXT NOT NULL DEFAULT '';
		`,
	},
	{
		version: 10,
		name:    "org_roles",
		sql: `
			CREATE TABLE IF NOT EXISTS org_roles (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				reports_to TEXT NOT NULL DEFAULT '',
				filled_by TEXT,
				filled_type TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);
		`,
	},
	{
		version: 11,
		name:    "integrations",
		sql: `
			CREATE TABLE IF NOT EXISTS webhook_hooks (
				id TEXT PRIMARY KEY,
				token TEXT UNIQUE NOT NULL,
				channel_id TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);

			CREATE TABLE IF NOT EXISTS webhook_events (
				id TEXT PRIMARY KEY,
				hook_id TEXT NOT NULL REFERENCES webhook_hooks(id),
				remote_addr TEXT NOT NULL DEFAULT '',
				payload TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'received',
				error TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);
			CREATE INDEX IF NOT EXISTS idx_webhook_events_hook ON webhook_events(hook_id, created_at);

			CREATE TABLE IF NOT EXISTS channel_integrations (
				id TEXT PRIMARY KEY,
				channel_id TEXT NOT NULL,
				source_type TEXT NOT NULL,
				source_key TEXT NOT NULL,
				label TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				UNIQUE(source_type, source_key)
			);
			CREATE INDEX IF NOT EXISTS idx_channel_integrations_lookup ON channel_integrations(source_type, source_key);

			CREATE TABLE IF NOT EXISTS email_threads (
				id TEXT PRIMARY KEY,
				message_id TEXT UNIQUE NOT NULL,
				channel_id TEXT NOT NULL,
				subject TEXT NOT NULL DEFAULT '',
				participants TEXT NOT NULL DEFAULT '[]',
				last_reply_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);
			CREATE INDEX IF NOT EXISTS idx_email_threads_msgid ON email_threads(message_id);
		`,
	},
	{
		version: 12,
		name: "mcp_servers",
		sql: `
			CREATE TABLE IF NOT EXISTS mcp_servers (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				transport TEXT NOT NULL DEFAULT 'stdio',
				command TEXT NOT NULL DEFAULT '',
				args TEXT NOT NULL DEFAULT '[]',
				url TEXT NOT NULL DEFAULT '',
				env TEXT NOT NULL DEFAULT '{}',
				headers TEXT NOT NULL DEFAULT '{}',
				enabled INTEGER NOT NULL DEFAULT 1,
				tool_prefix TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
				updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
			);
		`,
	},
	{
		version: 13,
		name:    "message_metadata",
		sql: `
			ALTER TABLE messages ADD COLUMN metadata TEXT NOT NULL DEFAULT '{}';
		`,
	},
	{
		version: 14,
		name:    "folders_and_file_metadata",
		sql: `
			CREATE TABLE IF NOT EXISTS folders (
				id TEXT PRIMARY KEY,
				parent_id TEXT,
				name TEXT NOT NULL,
				created_by TEXT NOT NULL,
				is_private INTEGER NOT NULL DEFAULT 0,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
				updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
			);
			CREATE INDEX IF NOT EXISTS idx_folders_parent ON folders(parent_id);
			ALTER TABLE files ADD COLUMN folder_id TEXT;
			ALTER TABLE files ADD COLUMN is_private INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE files ADD COLUMN description TEXT NOT NULL DEFAULT '';
			CREATE INDEX IF NOT EXISTS idx_files_folder ON files(folder_id);
		`,
	},
	{
		version: 15,
		name:    "task_position",
		sql: `
			ALTER TABLE tasks ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
		`,
	},
	{
		version: 16,
		name:    "agent_behavior_config",
		sql: `
			ALTER TABLE agents ADD COLUMN behavior_config TEXT NOT NULL DEFAULT '{}';
		`,
	},
	{
		version: 17,
		name:    "documents_folder_id",
		sql: `
			ALTER TABLE documents ADD COLUMN folder_id TEXT NOT NULL DEFAULT '';
			CREATE INDEX IF NOT EXISTS idx_documents_folder ON documents(folder_id);
		`,
	},
	{
		version: 18,
		name:    "calendar_events",
		sql: `
			CREATE TABLE IF NOT EXISTS calendar_events (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				location TEXT NOT NULL DEFAULT '',
				start_time TEXT NOT NULL,
				end_time TEXT NOT NULL,
				all_day INTEGER NOT NULL DEFAULT 0,
				recurrence_rule TEXT NOT NULL DEFAULT '',
				recurrence_parent_id TEXT,
				color TEXT NOT NULL DEFAULT '',
				calendar TEXT NOT NULL DEFAULT 'default',
				created_by TEXT NOT NULL,
				attendees TEXT NOT NULL DEFAULT '[]',
				reminders TEXT NOT NULL DEFAULT '[]',
				channel_id TEXT,
				status TEXT NOT NULL DEFAULT 'confirmed',
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
				updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
			);
			CREATE INDEX IF NOT EXISTS idx_cal_events_time ON calendar_events(start_time, end_time);
			CREATE INDEX IF NOT EXISTS idx_cal_events_creator ON calendar_events(created_by);

			CREATE TABLE IF NOT EXISTS calendar_reminders_sent (
				id TEXT PRIMARY KEY,
				event_id TEXT NOT NULL,
				reminder_key TEXT NOT NULL UNIQUE,
				sent_at TEXT NOT NULL
			);
		`,
	},
	{
		version: 19,
		name:    "member_colors",
		sql: `
			ALTER TABLE members ADD COLUMN color TEXT NOT NULL DEFAULT '';
		`,
	},
	{
		version: 20,
		name:    "workspace_models",
		sql: `
			CREATE TABLE IF NOT EXISTS workspace_models (
				id TEXT PRIMARY KEY,
				display_name TEXT NOT NULL,
				provider TEXT NOT NULL,
				context_length INTEGER DEFAULT 0,
				supports_tools BOOLEAN DEFAULT FALSE,
				pricing_prompt TEXT NOT NULL DEFAULT '0',
				pricing_completion TEXT NOT NULL DEFAULT '0',
				added_by TEXT NOT NULL,
				added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
			);
		`,
	},
	{
		version: 21,
		name:    "reply_threads_and_favorites",
		sql: `
			ALTER TABLE messages ADD COLUMN parent_id TEXT REFERENCES messages(id);
			CREATE INDEX IF NOT EXISTS idx_messages_parent ON messages(parent_id);
			ALTER TABLE channel_reads ADD COLUMN is_favorite BOOLEAN NOT NULL DEFAULT FALSE;
		`,
	},
	{
		version: 22,
		name:    "free_models",
		sql: `
			CREATE TABLE IF NOT EXISTS free_models (
				model_id TEXT PRIMARY KEY,
				display_name TEXT NOT NULL,
				priority INTEGER NOT NULL DEFAULT 0
			);
		`,
	},
	{
		version: 23,
		name:    "memory_source_and_fts",
		sql: `
			ALTER TABLE brain_memories ADD COLUMN source TEXT NOT NULL DEFAULT 'llm';

			CREATE VIRTUAL TABLE IF NOT EXISTS brain_memories_fts USING fts5(content, content='brain_memories', content_rowid='rowid');

			INSERT INTO brain_memories_fts(rowid, content) SELECT rowid, content FROM brain_memories;

			CREATE TRIGGER IF NOT EXISTS brain_memories_ai AFTER INSERT ON brain_memories BEGIN
				INSERT INTO brain_memories_fts(rowid, content) VALUES (new.rowid, new.content);
			END;

			CREATE TRIGGER IF NOT EXISTS brain_memories_ad AFTER DELETE ON brain_memories BEGIN
				INSERT INTO brain_memories_fts(brain_memories_fts, rowid, content) VALUES('delete', old.rowid, old.content);
			END;

			CREATE TRIGGER IF NOT EXISTS brain_memories_au AFTER UPDATE ON brain_memories BEGIN
				INSERT INTO brain_memories_fts(brain_memories_fts, rowid, content) VALUES('delete', old.rowid, old.content);
				INSERT INTO brain_memories_fts(rowid, content) VALUES (new.rowid, new.content);
			END;
		`,
	},
	{
		version: 24,
		name:    "brain_settings_log",
		sql: `
			CREATE TABLE IF NOT EXISTS brain_settings_log (
				id TEXT PRIMARY KEY,
				key TEXT NOT NULL,
				old_value TEXT NOT NULL DEFAULT '',
				new_value TEXT NOT NULL DEFAULT '',
				changed_by TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);
			CREATE INDEX IF NOT EXISTS idx_brain_settings_log_created ON brain_settings_log(created_at);
		`,
	},
	{
		version: 25,
		name:    "agent_image_model_and_channel_members",
		sql: `
			ALTER TABLE agents ADD COLUMN image_model TEXT NOT NULL DEFAULT '';

			CREATE TABLE IF NOT EXISTS channel_members (
				channel_id TEXT NOT NULL REFERENCES channels(id),
				member_id TEXT NOT NULL,
				added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				PRIMARY KEY (channel_id, member_id)
			);
			CREATE INDEX IF NOT EXISTS idx_channel_members_member ON channel_members(member_id);
		`,
	},
	{
		version: 26,
		name:    "favorited_at",
		sql: `
			ALTER TABLE channel_reads ADD COLUMN favorited_at TEXT NOT NULL DEFAULT '';
		`,
	},
	{
		version: 27,
		name:    "brain_web_search_tool",
		sql: `
			UPDATE agents SET tools = REPLACE(tools, '"search_messages"', '"search_workspace"')
				WHERE tools LIKE '%search_messages%';
			UPDATE agents SET tools = REPLACE(tools, '"delegate_to_agent"]', '"delegate_to_agent","web_search","fetch_url"]')
				WHERE is_system = 1 AND tools NOT LIKE '%web_search%';
		`,
	},
	{
		version: 28,
		name:    "activity_stream",
		sql: `
			CREATE TABLE IF NOT EXISTS activity_stream (
				id TEXT PRIMARY KEY,
				pulse_type TEXT NOT NULL,
				actor_id TEXT NOT NULL,
				actor_name TEXT NOT NULL,
				channel_id TEXT DEFAULT '',
				entity_id TEXT DEFAULT '',
				summary TEXT NOT NULL,
				detail TEXT DEFAULT '',
				source TEXT NOT NULL DEFAULT 'user',
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);
			CREATE INDEX IF NOT EXISTS idx_activity_created ON activity_stream(created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_activity_type ON activity_stream(pulse_type, created_at DESC);
		`,
	},
	{
		version: 29,
		name:    "brain_web_search_tool",
		sql: `
			UPDATE agents SET tools = REPLACE(tools, '"search_messages"', '"search_workspace"')
				WHERE tools LIKE '%search_messages%';
			UPDATE agents SET tools = REPLACE(tools, '"delegate_to_agent"]', '"delegate_to_agent","web_search","fetch_url"]')
				WHERE is_system = 1 AND tools NOT LIKE '%web_search%';
		`,
	},
	{
		version: 30,
		name:    "consolidate_message_activity",
		sql: `
			-- Delete all individual "sent a message" entries except the newest per actor+channel per 10-min window.
			-- First, keep the most recent entry per group and delete the rest.
			DELETE FROM activity_stream WHERE pulse_type = 'message.sent' AND id NOT IN (
				SELECT id FROM (
					SELECT id, ROW_NUMBER() OVER (
						PARTITION BY actor_id, channel_id,
						CAST(strftime('%s', created_at) AS INTEGER) / 600
						ORDER BY created_at DESC
					) AS rn
					FROM activity_stream
					WHERE pulse_type = 'message.sent'
				) WHERE rn = 1
			);
			-- Update remaining message entries to show count
			UPDATE activity_stream SET detail = '1' WHERE pulse_type = 'message.sent' AND (detail IS NULL OR detail = '');
		`,
	},
	{
		version: 31,
		name:    "expected_output_and_memory_importance",
		sql: `
			ALTER TABLE tasks ADD COLUMN expected_output TEXT NOT NULL DEFAULT '';
			ALTER TABLE brain_memories ADD COLUMN importance REAL NOT NULL DEFAULT 0.5;
		`,
	},
	{
		version: 32,
		name:    "whatsapp_conversations",
		sql: `
			CREATE TABLE IF NOT EXISTS whatsapp_conversations (
				id TEXT PRIMARY KEY,
				channel_id TEXT NOT NULL,
				wa_id TEXT NOT NULL UNIQUE,
				phone TEXT NOT NULL DEFAULT '',
				profile_name TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'open',
				assigned_to TEXT NOT NULL DEFAULT '',
				window_expires_at TEXT,
				last_message_at TEXT,
				metadata TEXT NOT NULL DEFAULT '{}',
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
				updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
			);
			CREATE INDEX IF NOT EXISTS idx_wa_conv_waid ON whatsapp_conversations(wa_id);
			CREATE INDEX IF NOT EXISTS idx_wa_conv_status ON whatsapp_conversations(status);
			CREATE INDEX IF NOT EXISTS idx_wa_conv_channel ON whatsapp_conversations(channel_id);
		`,
	},
	{
		version: 33,
		name:    "social_pulses",
		sql: `
			CREATE TABLE IF NOT EXISTS social_pulses (
				id TEXT PRIMARY KEY,
				topic TEXT NOT NULL,
				query TEXT NOT NULL,
				raw_text TEXT NOT NULL DEFAULT '',
				citations TEXT NOT NULL DEFAULT '[]',
				summary TEXT NOT NULL DEFAULT '',
				sentiment_score INTEGER NOT NULL DEFAULT 50,
				themes TEXT NOT NULL DEFAULT '[]',
				key_posts TEXT NOT NULL DEFAULT '[]',
				recommendations TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'pending',
				created_by TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_social_pulses_created ON social_pulses(created_at);
		`,
	},
	{
		version: 34,
		name:    "simplify_roles",
		sql:     `UPDATE members SET role = 'member' WHERE role NOT IN ('admin', 'member', 'guest', 'agent');`,
	},
	{
		version: 35,
		name:    "drop_whatsapp_conversations",
		sql:     `DROP TABLE IF EXISTS whatsapp_conversations;`,
	},
	{
		version: 36,
		name:    "memory_organizational_upgrade",
		sql: `
			ALTER TABLE brain_memories ADD COLUMN confidence REAL NOT NULL DEFAULT 0.5;
			ALTER TABLE brain_memories ADD COLUMN valid_until TEXT;
			ALTER TABLE brain_memories ADD COLUMN superseded_by TEXT;
			ALTER TABLE brain_memories ADD COLUMN participants TEXT NOT NULL DEFAULT '';
			ALTER TABLE brain_memories ADD COLUMN metadata TEXT NOT NULL DEFAULT '{}';
			ALTER TABLE brain_memories ADD COLUMN summary TEXT NOT NULL DEFAULT '';

			-- Backfill: set confidence = importance for existing rows
			UPDATE brain_memories SET confidence = importance;
		`,
	},
	{
		version: 37,
		name:    "task_scheduling",
		sql: `
			ALTER TABLE tasks ADD COLUMN scheduled_at TEXT;
			ALTER TABLE tasks ADD COLUMN agent_id TEXT;
			ALTER TABLE tasks ADD COLUMN recurrence_rule TEXT NOT NULL DEFAULT '';
		`,
	},
	{
		version: 38,
		name:    "calendar_event_agent",
		sql: `
			ALTER TABLE calendar_events ADD COLUMN agent_id TEXT NOT NULL DEFAULT '';
		`,
	},
	{
		version: 39,
		name:    "calendar_event_model",
		sql: `
			ALTER TABLE calendar_events ADD COLUMN model TEXT NOT NULL DEFAULT '';
		`,
	},
	{
		version: 40,
		name:    "calendar_execution_tracking",
		sql: `
			ALTER TABLE calendar_events ADD COLUMN executed_at TEXT NOT NULL DEFAULT '';
			ALTER TABLE calendar_events ADD COLUMN execution_message_id TEXT NOT NULL DEFAULT '';
			ALTER TABLE brain_action_log ADD COLUMN calendar_event_id TEXT NOT NULL DEFAULT '';
		`,
	},
	{
		version: 41,
		name:    "fix_agent_event_end_times",
		sql: `
			UPDATE calendar_events
			SET end_time = datetime(start_time, '+1 hour')
			WHERE agent_id != '' AND end_time < start_time;
		`,
	},
	{
		version: 42,
		name:    "social_pulse_enriched_fields",
		sql: `
			ALTER TABLE social_pulses ADD COLUMN predictions TEXT NOT NULL DEFAULT '[]';
			ALTER TABLE social_pulses ADD COLUMN risks TEXT NOT NULL DEFAULT '[]';
			ALTER TABLE social_pulses ADD COLUMN competitive_mentions TEXT NOT NULL DEFAULT '[]';
			ALTER TABLE social_pulses ADD COLUMN audience_breakdown TEXT NOT NULL DEFAULT '{}';
			ALTER TABLE social_pulses ADD COLUMN source_breakdown TEXT NOT NULL DEFAULT '{}';
		`,
	},
	{
		version: 43,
		name:    "living_briefs",
		sql: `
			CREATE TABLE IF NOT EXISTS living_briefs (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL,
				template TEXT NOT NULL DEFAULT 'custom',
				topic TEXT NOT NULL DEFAULT '',
				content TEXT NOT NULL DEFAULT '',
				generated_at DATETIME,
				schedule TEXT NOT NULL DEFAULT 'manual',
				schedule_time TEXT NOT NULL DEFAULT '',
				created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				updated_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);
		`,
	},
	{
		version: 44,
		name:    "brief_sharing",
		sql: `
			ALTER TABLE living_briefs ADD COLUMN share_token TEXT;
			ALTER TABLE living_briefs ADD COLUMN is_public INTEGER NOT NULL DEFAULT 0;
		`,
	},
	{
		version: 45,
		name:    "llm_usage_tracking",
		sql: `
			CREATE TABLE IF NOT EXISTS llm_usage (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				model TEXT NOT NULL,
				input_tokens INTEGER NOT NULL DEFAULT 0,
				output_tokens INTEGER NOT NULL DEFAULT 0,
				cost_usd REAL NOT NULL DEFAULT 0,
				action_type TEXT NOT NULL,
				channel_id TEXT,
				member_name TEXT,
				created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			);
			CREATE INDEX idx_llm_usage_created ON llm_usage(created_at);
			CREATE INDEX idx_llm_usage_action ON llm_usage(action_type);
		`,
	},
}

var workspaceMigrations46 = migration{
	version: 46,
	name:    "reflection_history",
	sql: `
		CREATE TABLE IF NOT EXISTS reflection_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			period TEXT NOT NULL DEFAULT 'daily',
			content TEXT NOT NULL,
			created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		);
		CREATE INDEX idx_reflection_history_created ON reflection_history(created_at);
	`,
}

var workspaceMigrations47 = migration{
	version: 47,
	name:    "knowledge provenance",
	sql: `
		ALTER TABLE brain_knowledge ADD COLUMN source_url TEXT DEFAULT '';
		ALTER TABLE brain_memories ADD COLUMN confidence_reason TEXT DEFAULT '';
	`,
}

var workspaceMigrations48 = migration{
	version: 48,
	name:    "thread_context",
	sql: `
		CREATE TABLE IF NOT EXISTS thread_context (
			parent_id TEXT PRIMARY KEY REFERENCES messages(id),
			channel_id TEXT NOT NULL,
			topic TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			participant_count INTEGER NOT NULL DEFAULT 0,
			message_count INTEGER NOT NULL DEFAULT 0,
			last_activity_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		);
	`,
}

var workspaceMigrations49 = migration{
	version: 49,
	name:    "task_recurrence_tracking",
	sql: `
		ALTER TABLE tasks ADD COLUMN run_count INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE tasks ADD COLUMN last_run_at TEXT;
	`,
}

var workspaceMigrations50 = migration{
	version: 50,
	name:    "task_recurrence_end",
	sql: `
		ALTER TABLE tasks ADD COLUMN recurrence_end TEXT NOT NULL DEFAULT '';
	`,
}

var workspaceMigrations51 = migration{
	version: 51,
	name:    "task_runs_and_last_run_status",
	sql: `
		CREATE TABLE IF NOT EXISTS task_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'success',
			output TEXT NOT NULL DEFAULT '',
			message_id TEXT NOT NULL DEFAULT '',
			channel_id TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		);
		CREATE INDEX idx_task_runs_task ON task_runs(task_id);
		ALTER TABLE tasks ADD COLUMN last_run_status TEXT NOT NULL DEFAULT '';
	`,
}

var workspaceMigrations52 = migration{
	version: 52,
	name:    "explicit_channel_membership",
	sql: `
		-- Add is_default flag to channels (auto-join for new workspace members)
		ALTER TABLE channels ADD COLUMN is_default BOOLEAN NOT NULL DEFAULT FALSE;

		-- Mark #general as default
		UPDATE channels SET is_default = TRUE WHERE name = 'general' AND type = 'public';

		-- Add role column to channel_members
		ALTER TABLE channel_members ADD COLUMN role TEXT NOT NULL DEFAULT 'member';

		-- Backfill: add all human members to all public/private channels
		INSERT OR IGNORE INTO channel_members (channel_id, member_id, role)
		SELECT c.id, m.id, 'member'
		FROM channels c
		CROSS JOIN members m
		WHERE c.type IN ('public', 'private')
		  AND c.archived = FALSE
		  AND m.role NOT IN ('brain', 'agent');

		-- Backfill: add Brain/agents ONLY to channels where they have sent messages
		INSERT OR IGNORE INTO channel_members (channel_id, member_id, role)
		SELECT DISTINCT msg.channel_id, msg.sender_id, 'bot'
		FROM messages msg
		JOIN members m ON msg.sender_id = m.id
		WHERE m.role IN ('brain', 'agent')
		  AND msg.deleted = FALSE
		  AND msg.channel_id IN (SELECT id FROM channels WHERE archived = FALSE AND type NOT IN ('dm'));
	`,
}

var workspaceMigrations53 = migration{
	version: 53,
	name:    "pinned_messages",
	sql: `
		CREATE TABLE IF NOT EXISTS pinned_messages (
			id TEXT PRIMARY KEY,
			channel_id TEXT NOT NULL REFERENCES channels(id),
			message_id TEXT NOT NULL REFERENCES messages(id),
			pinned_by TEXT NOT NULL,
			pinned_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			UNIQUE(channel_id, message_id)
		);
		CREATE INDEX IF NOT EXISTS idx_pinned_channel ON pinned_messages(channel_id);
	`,
}

var workspaceMigrations54 = migration{
	version: 54,
	name:    "notifications and notification preferences",
	sql: `
		CREATE TABLE IF NOT EXISTS notifications (
			id TEXT PRIMARY KEY,
			recipient_id TEXT NOT NULL,
			type TEXT NOT NULL,
			title TEXT NOT NULL,
			body TEXT NOT NULL DEFAULT '',
			link TEXT NOT NULL DEFAULT '',
			source_id TEXT NOT NULL DEFAULT '',
			actor_id TEXT NOT NULL DEFAULT '',
			read BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		);
		CREATE INDEX IF NOT EXISTS idx_notif_recipient ON notifications(recipient_id, read, created_at);

		CREATE TABLE IF NOT EXISTS notification_preferences (
			member_id TEXT NOT NULL,
			channel_id TEXT NOT NULL DEFAULT '__global__',
			level TEXT NOT NULL DEFAULT 'mentions',
			browser_push BOOLEAN NOT NULL DEFAULT TRUE,
			PRIMARY KEY (member_id, channel_id)
		);
	`,
}

var workspaceMigrations55 = migration{
	version: 55,
	name:    "brain memory v2: pinned memories and member association",
	sql: `
		ALTER TABLE brain_memories ADD COLUMN pinned BOOLEAN NOT NULL DEFAULT FALSE;
		ALTER TABLE brain_memories ADD COLUMN member_id TEXT NOT NULL DEFAULT '';
		CREATE INDEX IF NOT EXISTS idx_brain_memories_pinned ON brain_memories(pinned);
	`,
}

func init() {
	workspaceMigrations = append(workspaceMigrations, workspaceMigrations46, workspaceMigrations47, workspaceMigrations48, workspaceMigrations49, workspaceMigrations50, workspaceMigrations51, workspaceMigrations52, workspaceMigrations53, workspaceMigrations54, workspaceMigrations55)
}

func RunGlobal(db *sql.DB) error {
	if err := runMigrations(db, globalMigrations); err != nil {
		return err
	}
	return ensureSuperadmin(db)
}

// ensureSuperadmin promotes the configured superadmin email (env SUPERADMIN_EMAIL,
// default nruggieri@gmail.com). If the account exists, sets is_superadmin = TRUE.
// If no superadmin exists at all after that, the first account becomes superadmin.
func ensureSuperadmin(db *sql.DB) error {
	email := os.Getenv("SUPERADMIN_EMAIL")
	if email == "" {
		email = "nruggieri@gmail.com"
	}

	// Promote the configured email if the account exists
	res, err := db.Exec("UPDATE accounts SET is_superadmin = TRUE WHERE email = ? AND is_superadmin = FALSE", email)
	if err != nil {
		return fmt.Errorf("promoting superadmin: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}

	// Check if any superadmin exists
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM accounts WHERE is_superadmin = TRUE").Scan(&count); err != nil {
		return fmt.Errorf("checking superadmin: %w", err)
	}
	if count > 0 {
		return nil
	}

	// No superadmin at all — promote the first account
	_, err = db.Exec("UPDATE accounts SET is_superadmin = TRUE WHERE id = (SELECT id FROM accounts ORDER BY created_at ASC LIMIT 1)")
	if err != nil {
		return fmt.Errorf("promoting first account: %w", err)
	}
	return nil
}

// PromoteToSuperadmin promotes an account by email to superadmin. Used by CLI.
func PromoteToSuperadmin(db *sql.DB, email string) error {
	res, err := db.Exec("UPDATE accounts SET is_superadmin = TRUE WHERE email = ?", email)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("no account found with email %s", email)
	}
	return nil
}

func RunWorkspace(db *sql.DB) error {
	return runMigrations(db, workspaceMigrations)
}

func runMigrations(db *sql.DB, migs []migration) error {
	// Create migrations tracking table
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS _migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		)
	`); err != nil {
		return fmt.Errorf("creating migrations table: %w", err)
	}

	// Get current version
	var current int
	row := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM _migrations")
	if err := row.Scan(&current); err != nil {
		return fmt.Errorf("checking migration version: %w", err)
	}

	// Apply pending migrations
	for _, m := range migs {
		if m.version <= current {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := tx.Exec("INSERT INTO _migrations (version, name) VALUES (?, ?)", m.version, m.name); err != nil {
			tx.Rollback()
			return fmt.Errorf("recording migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}

	return nil
}
