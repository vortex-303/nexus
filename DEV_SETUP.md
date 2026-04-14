# Dev Setup — New Machine

Get the full Nexus project running from scratch on a fresh Mac or Linux box.

## Prerequisites

- **macOS:** [Homebrew](https://brew.sh) installed
- **Linux:** `apt` (Debian/Ubuntu) or `dnf` (Fedora/RHEL)
- A GitHub account with access to the `vortex-303/nexus` repo (private)

## Quick Start (3 commands)

```bash
gh auth login
gh repo clone vortex-303/nexus && cd nexus
./setup-dev.sh
```

`setup-dev.sh` handles everything: installs Go, Node, and build tools, fetches dependencies, builds the frontend and backend, and starts the dev server.

Open **http://localhost:3000** when it finishes.

## Step-by-Step (manual)

### 1. Install tools

**macOS:**
```bash
# Homebrew (if not installed)
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# GitHub CLI, Go, Node
brew install gh go node

# Xcode Command Line Tools (for gcc / SQLite CGO)
xcode-select --install
```

**Linux (Debian/Ubuntu):**
```bash
sudo apt-get update
sudo apt-get install -y golang nodejs npm build-essential
```

**Versions required:** Go 1.25+, Node.js 22+, gcc

### 2. Clone the repo

```bash
gh auth login                              # GitHub.com → HTTPS → Browser
gh repo clone vortex-303/nexus
cd nexus
```

### 3. Install dependencies

```bash
go mod download              # Go modules
npm --prefix web install     # SvelteKit / frontend
```

### 4. Build and run

```bash
make dev
```

This builds the SvelteKit frontend (`web/build/`), compiles the Go binary, and starts `./nexus serve --dev` on **http://localhost:3000**.

Other build commands:

| Command | What it does |
|---------|-------------|
| `make build` | Build the binary (`./nexus`) without running |
| `make web` | Build frontend only |
| `make clean` | Remove build artifacts and `~/.nexus/` data |
| `go build -tags "sqlite_fts5" -o nexus ./cmd/nexus/` | Backend only |

### 5. Set up AI Brain (optional)

1. Open your workspace → Brain tab → Settings
2. Add your [OpenRouter API key](https://openrouter.ai/keys)
3. `@Brain` in any channel to start chatting with the AI

### 6. Work with Claude Code

Install Claude Code on the new machine so you can develop with AI assistance:

```bash
brew install --cask claude-code
cd ~/nexus
claude
```

## Data Directory

All runtime data lives in `~/.nexus/`:

```
~/.nexus/
  nexus.db                    # Global database (accounts, workspaces)
  workspaces/
    <slug>/
      workspace.db            # Per-workspace database
      brain/skills/           # Brain skill files
      blobs/                  # Uploaded files (content-addressed)
```

- **Fresh start:** just run `make dev` — an empty workspace is created on first launch.
- **Copy existing data:** transfer `~/.nexus/` from another machine to get the same workspaces, messages, and brain memories.
- **Backup:** `cp -r ~/.nexus/ ~/nexus-backup/`

## Configuration

Optional — Nexus works out of the box with zero config.

```toml
# ~/.nexus/nexus.toml
listen = ":8080"
data_dir = "~/.nexus"
domain = "nexus.mycompany.com"   # Enables auto-TLS via Let's Encrypt
```

Config layers (each overrides the previous):
1. `~/.nexus/nexus.toml` or `./nexus.toml`
2. CLI flags: `--listen`, `--data-dir`, `--domain`, `--dev`
3. Environment variables: `LISTEN`, `DATA_DIR`, `DOMAIN`

## Production

The live instance runs on Fly.io:

```bash
fly deploy    # Deploy to nexus-workspace.fly.dev
```

## Troubleshooting

| Problem | Fix |
|---------|-----|
| `xcode-select: error: command line tools are already installed` | That's fine — you already have gcc. |
| `make: go: No such file or directory` | Restart your terminal after `brew install go` so PATH updates. |
| `cgo: C compiler "gcc" not found` | Run `xcode-select --install` and finish the dialog. |
| Port 3000 already in use | `make build && ./nexus serve --listen :3001 --dev` |
| `npm ERR! ERESOLVE` in web/ | `rm -rf web/node_modules web/package-lock.json && npm --prefix web install` |
