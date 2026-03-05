# Nexus

A shared AI brain for your team — instant, private, self-hosted.

Chat, tasks, docs, and an AI Brain that remembers everything — in a single binary you own completely.

## Features

- **Real-time chat** — Channels, DMs, markdown, file sharing, @mentions
- **AI Brain** — Persistent memory, tool calling, knowledge base, heartbeat scheduler
- **Custom agents** — 9 templates or define your own in markdown. Each gets tools, skills, and a role
- **Tasks** — Create from conversations, assign, track. Brain follows up on deadlines
- **Documents** — Rich editor with code blocks, checklists, and auto-save
- **MCP tools** — Extend with web search, databases, APIs — any MCP server
- **Roles & permissions** — 9 roles, 31 permissions, org chart with hierarchy
- **Integrations** — Webhooks, inbound email (SMTP), Telegram bot
- **Self-hosted** — Single Go binary. SQLite. Zero external dependencies

## Quick Start

### Cloud

Visit [nexus-workspace.fly.dev](https://nexus-workspace.fly.dev) — name your workspace and start.

### Self-Host

```bash
# Install
curl -fsSL https://raw.githubusercontent.com/vortex-303/nexus/main/install.sh | sh

# Run
nexus serve
# Nexus running at http://localhost:8080
```

### Docker

```bash
docker run -p 8080:8080 -v nexus_data:/data ghcr.io/vortex-303/nexus
```

### Build from Source

```bash
git clone https://github.com/vortex-303/nexus.git
cd nexus
make dev    # Builds web + Go, runs on http://localhost:3000
```

**Requirements:** Go 1.25+, Node.js 22+, gcc (for SQLite CGO)

## Configuration

Nexus loads config from three layers (each overrides the previous):

1. `~/.nexus/nexus.toml` or `./nexus.toml`
2. CLI flags: `--listen`, `--data-dir`, `--domain`, `--dev`
3. Environment variables: `LISTEN`, `DATA_DIR`, `DOMAIN`, `SMTP_LISTEN`

```toml
# ~/.nexus/nexus.toml
listen = ":8080"
data_dir = "~/.nexus"
domain = "nexus.mycompany.com"   # Enables auto-TLS via Let's Encrypt
```

### Auto-TLS

Set a `--domain` and Nexus will automatically provision Let's Encrypt certificates:

```bash
nexus serve --domain nexus.mycompany.com
```

## Data

All data lives in `~/.nexus/` (or `DATA_DIR`):

```
~/.nexus/
  nexus.db                    # Global database (accounts, workspaces)
  workspaces/
    <slug>/
      workspace.db            # Per-workspace database
      brain/skills/           # Brain skill files
      blobs/                  # Uploaded files (content-addressed)
```

Back up with `cp -r ~/.nexus/ ~/nexus-backup/`.

## Brain Setup

1. Open your workspace → Brain tab → Settings
2. Add your [OpenRouter API key](https://openrouter.ai/keys)
3. @Brain in any channel to start

The Brain reads every message, extracts facts and decisions into memory, and responds with context from the full workspace history.

## Documentation

- [ARCHITECTURE.md](ARCHITECTURE.md) — Technical architecture reference
- [SPEC.md](SPEC.md) — Product vision and specification
- [PLAN.md](PLAN.md) — Current state, roadmap, and next steps

## License

[AGPL-3.0](LICENSE)
