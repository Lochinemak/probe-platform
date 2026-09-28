# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A self-hosted multi-node network probing platform (itdog.cn-style). One Go server binary (`probe-server`) embeds a Vue dashboard and stores history in SQLite; many Go agents (`probe-agent`) on cloud VMs, home NAS boxes, OpenWrt routers and Windows PCs keep an **outbound** WebSocket to the server, run ping / tcping / http / mtr / dns tasks and stream results back. UI text and most user-facing strings are Chinese (zh-CN); keep new UI strings in Chinese and match the existing tone.

## Commands

```bash
make web            # npm install + vite build → web/dist (REQUIRED before any server-side go build/vet/test, see below)
make build          # web + bin/probe-server + bin/probe-agent for the host platform
make agents-all     # cross-compile agents for every supported OS/arch into bin/ (+ linux server builds)
make test           # go test ./...
make lint           # go vet ./... && gofmt -l ./cmd ./internal
make run-server     # go run ./cmd/server --data ./data --log-level debug --agents-dir ./bin
make run-agent      # go run ./cmd/agent against http://localhost:8080; needs PROBE_TOKEN=<node token> (create the node on the 节点 page first)
make geoip-db       # download ip2region xdb to data/ (MTR hop geo labels)
cd web && npm run dev   # Vite on :5173, proxies /api and /ws to :8080
```

Single test / package:

```bash
go test ./internal/server -run TestSessionAuth -v
go test ./internal/probe
```

Gotchas:

- `web/embed.go` has `//go:embed all:dist`. If `web/dist` does not exist, `go build ./cmd/server`, `go vet ./...` and `go test ./...` all fail with "pattern all:dist: no matching files found". Run `make web` once first (CI does this). `go build ./cmd/agent` does not need it.
- Agent code has OS-specific files (`cmd/agent/service_windows.go` vs `service_other.go`, `internal/agent/restart_{unix,windows}.go`, `internal/probe/mtr_tcp_{unix,windows}.go`). After touching them, cross-check with `GOOS=windows GOARCH=amd64 go build ./cmd/agent`.
- Tests need no external network: probe tests spin up loopback listeners and `t.Skip` when they cannot; server tests use `httptest` and a temp SQLite file. `internal/server/geoip_test.go` is skipped unless `PROBE_TEST_XDB` points at an xdb file. Test helpers (`discardLogger`, `emptyFS`) live in `internal/server/helpers_test.go`.
- Version is injected with `-ldflags -X probe-platform/internal/buildinfo.Version=…` (Makefile / Dockerfiles do it). A `dev` version never triggers agent self-update; only real builds do.
- `.claude/launch.json` runs `./bin/probe-server`, so `make build` must have run before previewing.
- Local `make run-server` uses `--agents-dir ./bin`, but the install-script endpoints (`/install-agent.sh` etc.) read those scripts from the same dir; in Docker the Dockerfile copies `deploy/*.sh` and `.ps1` there. Locally they 404 unless you copy them into `bin/`.
- CI (`.github/workflows/ci.yml`): `make web`, `go vet ./...`, `go test ./...`, `make server agent`. Push to `main` also triggers `deploy.yml` (builds `ghcr.io/lochinemak/probe-{server,agent}` tagged `sha-<7>` and SSH-triggers `deploy/prod/deploy.sh` on the production host). Tags `v*` run `release.yml` (binaries to GitHub Releases + images).

## Architecture

### Shared wire types: `internal/protocol`

Single source of truth for every JSON shape crossing a boundary: WebSocket frames (`Message` envelope; agent→server `hello|progress|result`, server→agent `welcome|task|cancel|update`), `Task`/`Params` (one flat struct holding every knob for every probe type; zero = probe default), per-type result payloads (`PingResult`, `HTTPResult`, `MTRResult`, `DNSResult`), monitoring types (`Monitor`, `Sample`, `MonitorAgentState`, `AlertEvent`, `NotifyChannel`), and the dashboard SSE `Event`. The Vue frontend consumes these shapes directly, so a field change here usually means a change in `web/src/derive.js` and a table component too.

### Agent: `cmd/agent` + `internal/agent` + `internal/probe`

- `internal/agent/client.go`: reconnect loop with backoff; per-session one writer goroutine (the only `WriteJSON` caller) and one reader. Sends `Hello` (name, version, os/arch/variant, capabilities, self_update flag), expects `Welcome`. Tasks run in goroutines gated by a semaphore (`PROBE_CONCURRENCY`, default 4). Progress frames are sent non-blocking and **dropped when the send buffer is full**; the final `Result` always carries the complete data, and the frontend is written to cope with either.
- `internal/agent/token.go`: migration off the legacy shared token. If the server puts a node token in `Welcome.Token` (only for agents that set `Hello.TokenHandoff`, i.e. have a `Config.TokenFile`), the agent writes `probe-agent.token` next to its executable (KEY=VALUE: `PROBE_TOKEN`, `REPLACES_SHA256` = sha256 of the configured token it supersedes), switches in memory and reconnects at once. At startup the file wins only while the configured token still hashes to `REPLACES_SHA256`. It only switches once the file is on disk. `cmd/agent` sets `TokenFile` only when self-update is on (containers would lose it on recreation). The install scripts adopt the file when re-run with the shared token and delete it; `uninstall-agent.sh --keep-config` folds it into the kept env file.
- `internal/agent/update.go`: self-update. Server offers `Update{version,path,sha256,size}`; agent jitters 0–20 s, downloads next to its executable, verifies sha256, runs `version` on the new file, drains in-flight tasks (≤60 s), swaps atomically and re-execs (`restart_unix.go`); on Windows it exits and relies on the service recovery policy (`restart_windows.go`). Test seams are package vars (`executablePath`, `restartProcess`, `updateJitter`, `drainTimeout`).
- `internal/probe`: pure Go, no external binaries. `probe.Run` dispatches on `TaskType`; each probe fills defaults with `def()`/`clamp()` and enforces hard caps (count ≤100, hops ≤64, HTTP body ≤8 MiB). `ProgressFunc` kinds: `resolved` (IP string), `reply` (ping/tcping), `attempt` (http/dns), `hops` (full `[]MTRHop` snapshot per round). `common.go` `Resolve` rejects hostnames resolving into 198.18.0.0/15 (Clash fake-IP) with a Chinese explanatory error. `mtr.go` is one engine with three flavours (icmp/tcp/udp), all needing a raw ICMP socket. `caps.go` detects `tcp,http,icmp,icmp_raw,mtr,ipv6`; `PrivilegeHint()` gives per-OS advice.
- `cmd/agent/main.go`: subcommands `version`, `test <target>` / `test <type> <target> k=v…` (runs probes locally, no server), `service …` (Windows SCM). `--env-file` is applied to the environment *before* flags read their env defaults, so a KEY=VALUE file behaves exactly like exported vars.

### Server: `cmd/server` + `internal/server`

- **Wiring** (`cmd/server/main.go`): `OpenStore` → `NewGeoIP` → `NewAgentFiles` → `NewHub` → `LoadSettings` → `NewNotifier` → `NewScheduler` (registers itself as the hub's task-done hook) → `NewHandler` (hands the hub `settings.LegacyAgentToken`). `http.Server` deliberately has no WriteTimeout (SSE/WS are long-lived).
- **Config vs Settings**: `config.go` `Config` = flags/`PROBE_*` env, read once. `settings.go` `Settings` = runtime-editable values (guest access, admin user, bcrypt password hash, Logto, base URL, agent image, session secret/epoch, whether the legacy shared agent token is still accepted) stored in the `settings` key/value table. Env values only *seed* on first start; anything saved from the dashboard wins thereafter without restart. Request-time code must read `Settings`, not `cfg`, for these.
- **Hub** (`hub.go`): owns live `agentConn`s and in-memory `taskRun`s. `createTask` builds one `AgentResult` per target agent, sends `task` frames, arms a `cfg.TaskTimeout` timer. `handleProgress` appends to `AgentResult.Progress` (cap 1000, never persisted) and annotates MTR hops with GeoIP server-side; `handleResult` finalises and persists to SQLite. Subscribers get `snapshot → progress* → result* → done`; slow subscribers drop events. A janitor evicts finished runs after 10 min; `LoadTask` then serves them from the store. Agent disconnect fails that agent's pending results.
- **Node identity = per-node token.** `POST /api/agents` creates a row with id `agentIDFromName(name)` (fixed forever) and a random token stored in plaintext in `agents.token` (so the dashboard can show the install command again; `GET/POST /api/agents/{id}/token` shows/resets it). `Hub.authenticate` looks the bearer token up; the name the agent reports only updates the display name/labels. Same token again = same node (reinstall in place; a second live connection replaces the first). Reset token / delete node revoke it and `Hub.Kick` the live connection. `agents.auth` is `token` | `legacy` | `''` (created, never connected). **Legacy shared token** (`PROBE_AGENT_TOKEN`, else `data/agent_token` if present, never generated any more) is accepted only while enabled (`Settings.LegacyAgentToken`, toggled from the Agents page via `PUT /api/settings {legacy_agent_token}`, which `KickLegacy`s) and only for existing rows with `auth='legacy'` (all pre-upgrade rows are backfilled to legacy when the column is added), identified by name as before; those agents get `Welcome.Token` if they advertise `TokenHandoff`. Once a node connects with its own token (`auth='token'`) the shared token is refused for it.
- **Scheduler** (`scheduler.go`): ticks every 5 s, `DueMonitors` → `CreateTaskWithMonitor`; `onTaskDone` → `sampleFromResult` (collapses any result type to latency / loss / ok / status / reached / throughput) → `samples` table → per-(monitor, agent) alert state machine in `monitor_state` (N consecutive failures → `down` event + notify once; recovery → `up`) → `Notifier`. Monitor-run tasks have empty `Owner` and are hidden from History unless requested.
- **Store** (`store.go`, `store_monitors.go`, `store_settings.go`): modernc pure-Go SQLite, WAL, `SetMaxOpenConns(4)`. Schema is `CREATE TABLE IF NOT EXISTS` strings; columns added after the first release go through `ensureColumn` *before* the schema exec (indexes reference them). Follow that pattern for new columns. Retention: `PROBE_RETAIN_DAYS` for tasks/samples, `PROBE_MONITOR_TASK_RETAIN_HOURS` for per-run monitor details.
- **Auth** (`auth.go`, `oidc.go`): stateless HMAC-signed cookie `probe_session` (30 d; `SessionEpoch` bump on password change logs everyone out). Modes: no password and no Logto → *open*, everyone is admin; otherwise anonymous = guest (if enabled) with a `probe_guest` cookie that becomes the task `Owner` (`guest:<id>`), so guests only see their own tasks. Middlewares in `api.go`: `protect` (admin), `protectGuest` (guest allowed, params capped by `guestParams`, agents trimmed by `publicAgent`, never-connected nodes hidden), `protectAgentOrSession` (any node token / enabled legacy token via `Hub.checkToken`, or admin login).
- **HTTP API** (`api.go`, `api_monitors.go`): Go 1.22 method-pattern mux. `/` is an SPA fallback that serves `index.html` for unknown paths (tabs live in the URL path) and long-caches `/assets/*`. Errors go through `writeErr` as `{"error": "..."}`.
- **AgentFiles** (`agentfiles.go`): serves `probe-agent-<os>-<arch>[<variant>][.exe]` from `PROBE_AGENTS_DIR` with lazily cached sha256; key format `linux-armv7`, `windows-amd64`. The server Docker image builds all agents into `/usr/share/probe-platform/agents`.
- **GeoIP** (`geoip.go`): ip2region xdb (offline) for MTR hop labels, ip-api.com (online, optional) for agent public IP; `splitRegion` / `normalizeISP` map to Chinese ISP names.
- **Notifier** (`notify.go`): `ChannelTypes` map (type → config keys, sent to the dashboard form) and a `Send` switch per type. Adding a channel means both plus `typeLabels` in `web/src/views/Notify.vue`.

### Frontend: `web/`

Vue 3 + Vite, no router and no state library. `App.vue` holds a `TABS` map (path segment → label, admin flag) and syncs the active tab with `location.pathname`; admin-only tabs gate on the `/api/session` role. `api.js` is a thin `fetch` wrapper that dispatches `probe:unauthorized` on 401 (App shows the login) and `subscribe()` opens the task SSE stream. `derive.js` normalises an `AgentResult` from *either* streamed progress or final data into what the tables render, so live and historical views share one code path. `TaskResults.vue` dispatches per task type to `PingTable` / `HttpTable` / `MtrView` / `DnsTable`. Charts use uPlot; `palette.js` assigns colours by sorted agent id (never by rank) and has light/dark variants. Styling is plain CSS with variables in `style.css` (dark default, light via `prefers-color-scheme`).

### Cross-cutting checklists

Adding a probe param or type touches, in order: `protocol.Params` (+ `TaskType`/`Valid` for a type) → probe implementation and `probe.Run` → `runSelfTest` param parsing in `cmd/agent/main.go` → `guestParams` caps in `api.go` → `sampleFromResult` in `scheduler.go` → `Probe.vue` defaults, `derive.js`, the table component, `fmt.js` `typeLabel` → README params table.

Adding a runtime setting: `settingsValues`, `SettingsView`, `SettingsPatch`, seeding in `LoadSettings`, the apply logic in `Settings.Update`, and `web/src/views/Settings.vue` (exception: the legacy-token switch lives on `Agents.vue`, next to the migration status, and `/api/agents` returns its state as `legacy_token`).

## Deployment layout (for context when touching `deploy/`)

`deploy/` has Dockerfiles, compose files (generic, NAS variants under `nas/`), systemd unit, install/uninstall scripts for Linux, OpenWrt (procd) and Windows (PowerShell), and reverse-proxy examples. `deploy/prod/` is the live instance (`https://probe.geneyuriy.com`, Docker on a Tencent Cloud host at `/opt/probe-platform`, OpenResty TLS termination, images pulled via mirror `ghcr.91856478.xyz`). The agent image sets `PROBE_SELF_UPDATE=false`; containers update by pulling a new image (and therefore never take part in the automatic token handover: Docker nodes migrate by being recreated with their node token). The README (Chinese) is the user-facing reference for config variables, API examples and the guest/admin model; keep it in sync when behaviour changes.
