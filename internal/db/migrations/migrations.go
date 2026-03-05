package migrations

import (
	"database/sql"
	"fmt"
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
}

func RunGlobal(db *sql.DB) error {
	if err := runMigrations(db, globalMigrations); err != nil {
		return err
	}
	return seedSuperadmin(db)
}

// seedSuperadmin ensures nruggieri@gmail.com exists as a superadmin account.
func seedSuperadmin(db *sql.DB) error {
	const email = "nruggieri@gmail.com"
	var exists int
	err := db.QueryRow("SELECT COUNT(*) FROM accounts WHERE email = ?", email).Scan(&exists)
	if err != nil {
		return fmt.Errorf("checking superadmin: %w", err)
	}
	if exists == 0 {
		// Create account without password — must set password via register or login flow
		_, err = db.Exec(
			"INSERT INTO accounts (id, email, password_hash, display_name, is_superadmin) VALUES (?, ?, '', ?, TRUE)",
			"sa_"+email, email, "Nick Ruggieri",
		)
		if err != nil {
			return fmt.Errorf("seeding superadmin: %w", err)
		}
	} else {
		// Ensure existing account has superadmin flag
		_, err = db.Exec("UPDATE accounts SET is_superadmin = TRUE WHERE email = ?", email)
		if err != nil {
			return fmt.Errorf("updating superadmin: %w", err)
		}
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
