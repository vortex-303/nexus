# Plan: Distribution + Infrastructure — Docker, Litestream, Deploy Buttons, Bleve Search, Cron

## Context
Nexus is a working product deployed on Fly.io. To grow distribution and resilience, we need: published Docker images (so anyone can run it), backup (so data isn't lost), deploy buttons (so users can 1-click deploy), full-text search (so users can find anything), and proper scheduling (replacing ad-hoc tickers).

GitHub repo: `vortex-303/nexus` | Module: `github.com/nexus-chat/nexus`

---

## Feature 1: Docker Image on GHCR (XS)

Publish multi-arch Docker images to `ghcr.io/vortex-303/nexus` on tag push.

### New Files
- **`.github/workflows/docker.yml`** — Build + push workflow using `docker/build-push-action` with QEMU for `linux/amd64,linux/arm64`. Tags: semver + latest. Runs alongside existing `release.yml`.
- **`docker-compose.yml`** — Simple compose file for `docker compose up`

### Modified Files
- **`Dockerfile`** — Add OCI labels (`org.opencontainers.image.source`, `.description`, `.licenses`) to final stage

---

## Feature 2: Litestream SQLite Backup (S)

Continuous SQLite replication to S3-compatible storage via Litestream, activated by env var.

### Approach
- Bundle `litestream` binary in Docker image (from `litestream/litestream:0.3.13` multi-arch image)
- Entrypoint script: if `LITESTREAM_REPLICA_URL` is set → generate config + run `litestream replicate -exec "./nexus serve"`. If not set → run `./nexus serve` directly (zero behavior change for existing deploys)
- Auto-restore on first boot if DB doesn't exist

### Challenge: Dynamic workspace DBs
Workspace DBs are created on-demand. Entrypoint scans `/data/workspaces/*/workspace.db` at boot. New workspaces created after boot are replicated on next restart. Acceptable tradeoff.

### New Files
- **`scripts/entrypoint.sh`** — Smart entrypoint: detect `LITESTREAM_REPLICA_URL`, generate `litestream.yml` from discovered DBs, restore-if-needed, exec with or without litestream

### Modified Files
- **`Dockerfile`** — Add `COPY --from=litestream/litestream:0.3.13 /usr/local/bin/litestream /usr/local/bin/`, copy entrypoint, change CMD to `./entrypoint.sh`
- **`docker-compose.yml`** — Add commented-out litestream env vars

---

## Feature 3: Deploy Buttons (XS)

One-click deploy to Render + DigitalOcean. Fly.io instructions.

### New Files
- **`render.yaml`** — Render blueprint: web service, Docker runtime, starter plan, `/health` check, 1GB disk at `/data`
- **`.do/deploy.template.yaml`** — DO App Platform spec: GHCR image, port 8080, health check

### Modified Files
- **`README.md`** — Add deploy button badges (Render SVG, DO SVG) + Fly.io `fly launch --image` instructions

---

## Feature 4: Bleve Full-Text Search (M)

Replace SQL LIKE with bleve indexes. Add global search API + Cmd+K search modal.

### Architecture
- One bleve index per workspace at `{dataDir}/workspaces/{slug}/search.bleve`
- Index 4 types: messages, documents, tasks, knowledge
- Index on write (hook into existing create/update/delete handlers)
- Lazy backfill: on first workspace open, if `search.bleve` doesn't exist, index all existing data in a goroutine

### New Files
- **`internal/search/index.go`** (~150 lines) — `IndexManager` struct with `Open(slug)`, `IndexMessage()`, `IndexTask()`, `IndexDocument()`, `IndexKnowledge()`, `Delete()`, `Search()`, `Reindex()`, `CloseAll()`
- **`internal/server/search.go`** (~40 lines) — `handleSearch` HTTP handler: `GET /api/workspaces/{slug}/search?q=...&type=messages,docs,tasks,knowledge`
- **`web/src/lib/components/SearchModal.svelte`** (~150 lines) — Cmd+K modal with debounced search, results grouped by type, click-to-navigate

### Modified Files
- **`go.mod`** — Add `github.com/blevesearch/bleve/v2`
- **`internal/server/server.go`** — Add `search *search.IndexManager` field, init in `Run()`, close on shutdown, register search route
- **`internal/server/ws.go`** — After message INSERT/edit/delete, call `s.search.IndexMessage()` / `s.search.Delete()`
- **`internal/server/tasks.go`** — Index on create/update, delete on delete
- **`internal/server/documents.go`** — Index on create/update, delete on delete
- **`internal/server/brain_knowledge.go`** — Index on create, delete on delete
- **`internal/server/brain_tools.go`** — Replace `toolSearchMessages` LIKE query with `s.search.Search()` call
- **`web/src/lib/api.ts`** — Add `searchWorkspace(slug, query, types?)` function
- **`web/src/routes/(app)/w/[slug]/+page.svelte`** — Mount SearchModal, add Cmd+K listener

### Note
Keep existing LIKE search in `internal/brain/knowledge.go` as fallback for RAG context (it's internal, not user-facing, and knowledge bases are small).

---

## Feature 5: robfig/cron Scheduler (XS)

Replace custom `time.NewTicker` heartbeat runner with `robfig/cron`.

### Modified Files
- **`go.mod`** — Add `github.com/robfig/cron/v3`
- **`internal/server/server.go`** — Add `cron *cron.Cron` field, init + start in `Run()`, stop on shutdown
- **`internal/server/brain_heartbeat.go`** — Replace `startHeartbeatRunner()` with `scheduleHeartbeats()` that registers `s.cron.AddFunc("@every 1m", s.checkHeartbeats)`. Delete old ticker goroutine. Existing `checkHeartbeats()` + `ShouldRun()` logic unchanged.

---

## Implementation Order

1. **Cron** (XS) — smallest, isolated, unblocks nothing but gets cron library in
2. **Bleve Search** (M) — biggest user-facing feature, no external deps
3. **Docker/GHCR** (XS) — workflow + labels, needed before deploy buttons
4. **Litestream** (S) — modifies Dockerfile, test after GHCR workflow works
5. **Deploy Buttons** (XS) — 2 YAML files + README, depends on GHCR image

## npm Packages
None.

## New Go Dependencies
- `github.com/robfig/cron/v3`
- `github.com/blevesearch/bleve/v2`

## Verification
1. `go build ./cmd/nexus/` — builds clean with new deps
2. `npm run build` in `web/` — frontend builds with SearchModal
3. `make dev` → create workspace → send messages → create tasks → create docs → test Cmd+K search
4. `docker build .` — Dockerfile builds with litestream
5. `LITESTREAM_REPLICA_URL="" docker run ...` — runs without litestream (backward compat)
6. Tag push → verify GHCR image published + GoReleaser binaries still work
7. Test Render deploy button URL loads correctly
8. `fly deploy` — existing Fly.io deploy still works
