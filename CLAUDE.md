# Nexus — Development Guide

## Build Commands

```bash
make dev        # Build web + Go, run on :8080 (dev mode)
make build      # Build web + Go binary (./nexus)
make web        # Build frontend only (web/build/)
make clean      # Remove build artifacts + data dir

cd web && npm run build   # Frontend only
go build ./cmd/nexus/     # Backend only
fly deploy                # Deploy to Fly.io (production)
```

## Architecture

- **Single binary:** Go backend embeds SvelteKit static output via `//go:embed all:build` in `web/embed.go`
- **Database:** SQLite with WAL mode. 1 global DB (`nexus.db`) + 1 DB per workspace (`workspaces/{slug}/workspace.db`)
- **Frontend:** SvelteKit 2 / Svelte 5, static adapter, TypeScript
- **Real-time:** WebSocket hub per workspace, JSON envelope protocol
- **Auth:** JWT HS256, workspace-scoped tokens, 9 roles, 31 permissions
- **AI:** OpenRouter for LLM, Google Gemini for images, MCP for tool extensions

## Key Directories

- `cmd/nexus/` — CLI entry point
- `internal/server/` — All HTTP + WS handlers
- `internal/brain/` — Brain engine, OpenRouter, memory, skills, tools
- `internal/db/migrations/` — SQLite migrations (global + per-workspace)
- `internal/roles/` — RBAC role definitions and permission checker
- `internal/hub/` — WebSocket hub and protocol types
- `internal/mcp/` — MCP client manager
- `web/src/routes/(app)/` — SvelteKit pages (login, workspace, admin)
- `web/src/lib/` — API client, WebSocket client, stores, editor
- `web/static/landing.html` — Standalone marketing page (no SvelteKit)

## Conventions

- Go: standard library style, no frameworks. HTTP handlers are methods on `*Server`
- Frontend: Svelte 5 runes (`$state`, `$derived`), scoped `<style>` blocks
- CSS: custom properties defined in `web/src/app.css` (dark theme, orange accent)
- API: REST for CRUD, WebSocket for real-time events
- Database: migrations are sequential (v1, v2, ...) in `internal/db/migrations/migrations.go`
- Files: content-addressed blobs stored by SHA-256 hash
- Brain: system prompt built from definition files (SOUL.md, INSTRUCTIONS.md, etc.) + memories + skills + knowledge

## Important Patterns

- `//go:embed all:build` (not `build/*`) — the `all:` prefix is required to include `_app/` directory
- SvelteKit app routes live under `(app)/` layout group with `ssr = false`
- Landing page is plain HTML at `web/static/landing.html` — completely decoupled from SvelteKit
- Brain runs ONE pipeline (v4, in `internal/brain2/` + `internal/server/brain2.go`): a self-correcting tool loop (validation, timeouts, loop detection, cost cap, up to 10 rounds) + synthesizer with token streaming over `brain.chunk` WS events. Model-agnostic via OpenRouter/Gemini/Ollama/xAI (`makeBrainClient`)
- Brain has a persistent file memory at `<dataDir>/workspaces/<slug>/brain/memory` — five path-jailed tools (read/write/edit/glob/grep_memory), pinned.md + sender profile pre-injected each turn
- MCP managers are per-workspace, lazily initialized, stored in `sync.Map`
- WebSocket auth is via `?token=` query parameter
- Superadmin comes from the `SUPERADMIN_EMAIL` env var (first-user-is-admin fallback; `nexus admin promote <email>` CLI)

## Testing

`go test ./internal/brain/` covers the memory filesystem (path jail, round-trip). Otherwise verify manually:
1. `make dev` — builds and runs
2. Create workspace → send message → upload file → create task → create doc
3. Add OpenRouter key → @Brain in chat → verify tool calling works
4. `npm run build` in `web/` — frontend builds clean
5. `go build ./cmd/nexus/` — backend builds clean
