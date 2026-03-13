package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
	"github.com/nexus-chat/nexus/internal/mcp"
)

type MCPEnvVar struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	HelpURL     string `json:"help_url,omitempty"`
}

type MCPTemplate struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Category    string      `json:"category"`
	Transport   string      `json:"transport"`
	Command     string      `json:"command"`
	EnvVars     []MCPEnvVar `json:"env_vars"`
	Prefix      string      `json:"prefix"`
	Tier        string      `json:"tier"`
}

var mcpTemplates = []MCPTemplate{
	// Free tier — no signup needed
	{
		ID:          "web-search",
		Name:        "Web Search",
		Description: "Search the web via DuckDuckGo — no API key needed",
		Category:    "web",
		Transport:   "stdio",
		Command:     "npx -y duckduckgo-mcp-server",
		Prefix:      "ddg",
		Tier:        "free",
	},
	{
		ID:          "fetch",
		Name:        "Fetch",
		Description: "Fetch and extract content from any URL on the web",
		Category:    "web",
		Transport:   "stdio",
		Command:     "npx -y mcp-fetch-server",
		Prefix:      "fetch",
		Tier:        "free",
	},
	{
		ID:          "time",
		Name:        "Time & Timezone",
		Description: "Get current time and convert between timezones",
		Category:    "productivity",
		Transport:   "stdio",
		Command:     "npx -y mcp-time",
		Prefix:      "time",
		Tier:        "free",
	},
	{
		ID:          "sequential-thinking",
		Name:        "Sequential Thinking",
		Description: "Step-by-step reasoning and problem decomposition",
		Category:    "ai",
		Transport:   "stdio",
		Command:     "npx -y @modelcontextprotocol/server-sequential-thinking",
		Prefix:      "think",
		Tier:        "free",
	},
	{
		ID:          "memory",
		Name:        "Knowledge Graph",
		Description: "Persistent memory using a local knowledge graph",
		Category:    "ai",
		Transport:   "stdio",
		Command:     "npx -y @modelcontextprotocol/server-memory",
		Prefix:      "memory",
		Tier:        "free",
	},

	// --- API Key tier: Search ---
	{
		ID:          "brave-search",
		Name:        "Brave Search",
		Description: "Web and local search via the Brave Search API",
		Category:    "web",
		Transport:   "stdio",
		Command:     "npx -y @brave/brave-search-mcp-server",
		Prefix:      "brave",
		Tier:        "api_key",
		EnvVars: []MCPEnvVar{
			{Key: "BRAVE_API_KEY", Description: "Brave Search API key", Required: true, HelpURL: "https://brave.com/search/api/"},
		},
	},
	{
		ID:          "exa-search",
		Name:        "Exa Search",
		Description: "AI-native search with citations and content extraction",
		Category:    "web",
		Transport:   "stdio",
		Command:     "npx -y exa-mcp-server",
		Prefix:      "exa",
		Tier:        "api_key",
		EnvVars: []MCPEnvVar{
			{Key: "EXA_API_KEY", Description: "Exa API key", Required: true, HelpURL: "https://exa.ai/"},
		},
	},
	{
		ID:          "tavily-search",
		Name:        "Tavily Search",
		Description: "Research-grade web search optimized for AI agents",
		Category:    "web",
		Transport:   "stdio",
		Command:     "npx -y tavily-mcp",
		Prefix:      "tavily",
		Tier:        "api_key",
		EnvVars: []MCPEnvVar{
			{Key: "TAVILY_API_KEY", Description: "Tavily API key", Required: true, HelpURL: "https://tavily.com/"},
		},
	},
	{
		ID:          "firecrawl",
		Name:        "Firecrawl",
		Description: "Web scraping, crawling, and structured data extraction",
		Category:    "web",
		Transport:   "stdio",
		Command:     "npx -y firecrawl-mcp",
		Prefix:      "firecrawl",
		Tier:        "api_key",
		EnvVars: []MCPEnvVar{
			{Key: "FIRECRAWL_API_KEY", Description: "Firecrawl API key", Required: true, HelpURL: "https://firecrawl.dev/"},
		},
	},

	// --- API Key tier: Dev ---
	{
		ID:          "github",
		Name:        "GitHub",
		Description: "Manage repos, issues, PRs, and more via GitHub API",
		Category:    "dev",
		Transport:   "stdio",
		Command:     "npx -y @modelcontextprotocol/server-github",
		Prefix:      "github",
		Tier:        "api_key",
		EnvVars: []MCPEnvVar{
			{Key: "GITHUB_PERSONAL_ACCESS_TOKEN", Description: "GitHub personal access token", Required: true, HelpURL: "https://github.com/settings/tokens"},
		},
	},
	{
		ID:          "gitlab",
		Name:        "GitLab",
		Description: "Manage GitLab repos, issues, merge requests, and pipelines",
		Category:    "dev",
		Transport:   "stdio",
		Command:     "npx -y @modelcontextprotocol/server-gitlab",
		Prefix:      "gitlab",
		Tier:        "api_key",
		EnvVars: []MCPEnvVar{
			{Key: "GITLAB_PERSONAL_ACCESS_TOKEN", Description: "GitLab personal access token", Required: true, HelpURL: "https://gitlab.com/-/user_settings/personal_access_tokens"},
		},
	},

	// --- API Key tier: Productivity ---
	{
		ID:          "notion",
		Name:        "Notion",
		Description: "Search, read, and update Notion pages and databases",
		Category:    "productivity",
		Transport:   "stdio",
		Command:     "npx -y @notionhq/notion-mcp-server",
		Prefix:      "notion",
		Tier:        "api_key",
		EnvVars: []MCPEnvVar{
			{Key: "NOTION_TOKEN", Description: "Notion integration token", Required: true, HelpURL: "https://www.notion.so/my-integrations"},
		},
	},
	{
		ID:          "todoist",
		Name:        "Todoist",
		Description: "Manage tasks and projects in Todoist",
		Category:    "productivity",
		Transport:   "stdio",
		Command:     "npx -y todoist-mcp-server",
		Prefix:      "todoist",
		Tier:        "api_key",
		EnvVars: []MCPEnvVar{
			{Key: "TODOIST_API_TOKEN", Description: "Todoist API token", Required: true, HelpURL: "https://todoist.com/app/settings/integrations/developer"},
		},
	},
	{
		ID:          "google-calendar",
		Name:        "Google Calendar",
		Description: "View and manage Google Calendar events and schedules",
		Category:    "productivity",
		Transport:   "stdio",
		Command:     "npx -y google-calendar-mcp",
		Prefix:      "gcal",
		Tier:        "api_key",
		EnvVars: []MCPEnvVar{
			{Key: "GOOGLE_CLIENT_ID", Description: "Google OAuth client ID", Required: true, HelpURL: "https://console.cloud.google.com/apis/credentials"},
			{Key: "GOOGLE_CLIENT_SECRET", Description: "Google OAuth client secret", Required: true, HelpURL: "https://console.cloud.google.com/apis/credentials"},
			{Key: "GOOGLE_REDIRECT_URI", Description: "OAuth redirect URI (e.g. http://localhost:3000/oauth/callback)", Required: true},
		},
	},

	// --- API Key tier: Communication ---
	{
		ID:          "slack",
		Name:        "Slack",
		Description: "Read and send messages, manage channels in Slack",
		Category:    "communication",
		Transport:   "stdio",
		Command:     "npx -y @modelcontextprotocol/server-slack",
		Prefix:      "slack",
		Tier:        "api_key",
		EnvVars: []MCPEnvVar{
			{Key: "SLACK_BOT_TOKEN", Description: "Slack bot OAuth token (xoxb-...)", Required: true, HelpURL: "https://api.slack.com/apps"},
			{Key: "SLACK_TEAM_ID", Description: "Slack workspace/team ID", Required: true, HelpURL: "https://api.slack.com/apps"},
		},
	},
	{
		ID:          "resend",
		Name:        "Resend Email",
		Description: "Send emails via the Resend API",
		Category:    "communication",
		Transport:   "stdio",
		Command:     "npx -y resend-mcp",
		Prefix:      "resend",
		Tier:        "api_key",
		EnvVars: []MCPEnvVar{
			{Key: "RESEND_API_KEY", Description: "Resend API key", Required: true, HelpURL: "https://resend.com/api-keys"},
		},
	},

	// --- API Key tier: Knowledge ---
	{
		ID:          "deepwiki",
		Name:        "DeepWiki",
		Description: "Search and read any GitHub repo's wiki and documentation",
		Category:    "dev",
		Transport:   "stdio",
		Command:     "npx -y deepwiki-mcp",
		Prefix:      "wiki",
		Tier:        "free",
	},

	// --- API Key tier: Cloud Storage ---
	{
		ID:          "google-drive",
		Name:        "Google Drive",
		Description: "Search and read files from Google Drive",
		Category:    "data",
		Transport:   "stdio",
		Command:     "npx -y @modelcontextprotocol/server-gdrive",
		Prefix:      "gdrive",
		Tier:        "api_key",
		EnvVars: []MCPEnvVar{
			{Key: "GDRIVE_CLIENT_ID", Description: "Google OAuth client ID", Required: true, HelpURL: "https://console.cloud.google.com/apis/credentials"},
			{Key: "GDRIVE_CLIENT_SECRET", Description: "Google OAuth client secret", Required: true, HelpURL: "https://console.cloud.google.com/apis/credentials"},
		},
	},
	{
		ID:          "s3",
		Name:        "S3 / R2 Storage",
		Description: "Read and manage files in S3-compatible storage (AWS, Cloudflare R2)",
		Category:    "data",
		Transport:   "stdio",
		Command:     "npx -y s3-mcp-server",
		Prefix:      "s3",
		Tier:        "api_key",
		EnvVars: []MCPEnvVar{
			{Key: "S3_ENDPOINT", Description: "S3 endpoint URL", Required: true},
			{Key: "S3_ACCESS_KEY_ID", Description: "Access key ID", Required: true},
			{Key: "S3_SECRET_ACCESS_KEY", Description: "Secret access key", Required: true},
			{Key: "S3_BUCKET", Description: "Bucket name", Required: true},
		},
	},

	// --- Custom tier: Data ---
	{
		ID:          "postgres",
		Name:        "PostgreSQL",
		Description: "Query and explore PostgreSQL databases",
		Category:    "data",
		Transport:   "stdio",
		Command:     "npx -y @modelcontextprotocol/server-postgres",
		Prefix:      "pg",
		Tier:        "custom",
		EnvVars: []MCPEnvVar{
			{Key: "POSTGRES_CONNECTION", Description: "PostgreSQL connection string (e.g. postgresql://user:pass@host/db)", Required: true},
		},
	},
	{
		ID:          "filesystem",
		Name:        "Filesystem",
		Description: "Read, search, and manage files in allowed directories",
		Category:    "data",
		Transport:   "stdio",
		Command:     "npx -y @modelcontextprotocol/server-filesystem",
		Prefix:      "fs",
		Tier:        "custom",
		EnvVars: []MCPEnvVar{
			{Key: "FS_ALLOWED_DIRS", Description: "Comma-separated directory paths to allow access to", Required: true},
		},
	},
	{
		ID:          "sqlite",
		Name:        "SQLite",
		Description: "Query and explore SQLite databases",
		Category:    "data",
		Transport:   "stdio",
		Command:     "uvx mcp-server-sqlite",
		Prefix:      "sqlite",
		Tier:        "custom",
		EnvVars: []MCPEnvVar{
			{Key: "SQLITE_DB_PATH", Description: "Path to the SQLite database file", Required: true},
		},
	},
}

func (s *Server) handleListMCPTemplates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mcpTemplates)
}

// fixupMCPCommands patches stale uvx-based MCP server commands to their npx equivalents.
// This handles existing workspaces that were seeded before the template was fixed.
func (s *Server) fixupMCPCommands(slug string) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}

	fixes := map[string]struct{ cmd string; args string }{
		"uvx":  {"", ""},  // marker — we match on command="uvx"
	}
	_ = fixes

	// Map old command+args to new command+args
	type fix struct {
		oldCmd  string
		oldArgs string // JSON args containing the old identifier
		newCmd  string
		newArgs string
	}
	patches := []fix{
		{"uvx", `["duckduckgo-mcp","serve"]`, "npx", `["-y","duckduckgo-mcp-server"]`},
		{"uvx", `["mcp-server-fetch"]`, "npx", `["-y","mcp-fetch-server"]`},
		{"uvx", `["mcp-server-time"]`, "npx", `["-y","mcp-time"]`},
	}

	for _, p := range patches {
		wdb.DB.Exec(`UPDATE mcp_servers SET command = ?, args = ? WHERE command = ? AND args = ?`,
			p.newCmd, p.newArgs, p.oldCmd, p.oldArgs)
	}

	// Also fix if someone had the broken npx commands from the intermediate deploy
	intermediateFixes := []fix{
		{"npx", `["-y","@modelcontextprotocol/server-fetch"]`, "npx", `["-y","mcp-fetch-server"]`},
		{"npx", `["-y","@modelcontextprotocol/server-time"]`, "npx", `["-y","mcp-time"]`},
		{"npx", `["-y","@modelcontextprotocol/server-sqlite"]`, "npx", `["-y","mcp-server-sqlite"]`},
	}
	for _, p := range intermediateFixes {
		wdb.DB.Exec(`UPDATE mcp_servers SET command = ?, args = ? WHERE command = ? AND args = ?`,
			p.newCmd, p.newArgs, p.oldCmd, p.oldArgs)
	}
}

// seedFreeMCPServers adds all free-tier MCP server templates to a new workspace.
func (s *Server) seedFreeMCPServers(slug string) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		logger.WithCategory(logger.CatSystem).Error().Err(err).Str("workspace", slug).Msg("MCP seed: failed to open workspace")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var configs []mcp.ServerConfig

	for _, t := range mcpTemplates {
		if t.Tier != "free" {
			continue
		}

		serverID := "mcp_" + id.New()

		// Split command into command + args
		parts := strings.Fields(t.Command)
		cmd := parts[0]
		var args []string
		if len(parts) > 1 {
			args = parts[1:]
		}

		argsJSON, _ := json.Marshal(args)
		envJSON := []byte("{}")
		headersJSON := []byte("{}")

		_, err := wdb.DB.Exec(`
			INSERT INTO mcp_servers (id, name, transport, command, args, url, env, headers, enabled, tool_prefix, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, '', ?, ?, 1, ?, ?, ?)`,
			serverID, t.Name, t.Transport, cmd, string(argsJSON), string(envJSON), string(headersJSON), t.Prefix, now, now,
		)
		if err != nil {
			logger.WithCategory(logger.CatSystem).Error().Err(err).Str("template", t.Name).Msg("MCP seed: failed to insert")
			continue
		}

		configs = append(configs, mcp.ServerConfig{
			ID:        serverID,
			Name:      t.Name,
			Transport: t.Transport,
			Command:   cmd,
			Args:      args,
			Prefix:    t.Prefix,
			Enabled:   true,
		})
	}

	if len(configs) > 0 {
		logger.WithCategory(logger.CatSystem).Info().Int("count", len(configs)).Str("workspace", slug).Msg("seeded free MCP servers")
		// Connect them async via the MCP manager
		go func() {
			mgr := s.getMCPManager(slug)
			for _, cfg := range configs {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				if err := mgr.Connect(ctx, cfg); err != nil {
					logger.WithCategory(logger.CatSystem).Error().Err(err).Str("server", cfg.Name).Msg("MCP seed: connect failed")
				}
				cancel()
			}
		}()
	}
}
