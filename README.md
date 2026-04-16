# aor — Agent ORchestration

A Go workspace of small, composable tools for coordinating work across agents and auditing UI flows.

## Tools

| Binary | Purpose | Data |
|--------|---------|------|
| **[`ata`](#ata--agent-tasks)** | Task tracker — backlog, queue, epics, dependencies, tags | `~/.ata/ata.db` |
| **[`afl`](#afl--agent-flows)** | Flow & screenshot tracker for cross-platform UI parity | `~/.afl/afl.db`, `~/.afl/screenshots/` |
| **[`aor`](#aor--unified-server)** | Unified web server that hosts both `ata` and `afl` UIs | — |

All three are Go modules in a single workspace (`go.work`). Each CLI runs locally against its own SQLite database, or transparently proxies commands to a remote `aor serve` instance when a remote is configured.

## Install

```bash
make install     # builds and installs ata, afl, aor to $GOBIN
make check       # vet + test
```

Requires Go 1.23+.

## `ata` — Agent TAsks

Task manager scoped to a workspace (auto-detected from the git root). Tasks move `backlog → queue → in_progress → closed`; epics group child tasks and carry structured specs.

```bash
ata create "Refactor auth middleware" --tag backend
ata list                    # active tasks in this workspace
ata ready                   # queue tasks with no unresolved blockers
ata claim <id>              # take a task (sets in_progress)
ata close <id>              # mark complete
ata promote <id> --spec-file spec.md   # turn into an epic
ata dep add <task> <blocker>           # dependency
ata serve                   # htmx web UI on :4400
```

Full command reference: `ata help`, or `.claude/skills/ata/SKILL.md`.

## `afl` — Agent FLows

Tracks UI flows across platforms (web desktop/mobile, iOS, Android) by storing step-level screenshots. Flows mirror the structure of `specs/*/flows.md` and can be imported directly.

Entity hierarchy:

```
Workspace → Domain → Flow → Path (happy | alternate | error) → Step → Screenshot × Platform
```

```bash
afl domain create water --name "Water Tracking"
afl flow create water WATER-LOG-ENTRY "Add Water Entry"
afl flow import specs/water/flows.md      # parse flows.md → domain/flows/paths/steps

afl capture upload WATER-LOG-ENTRY 1 web-desktop shot.png --source playwright
afl capture batch WATER-LOG-ENTRY ios ./captures/  # 1.png, 2.png, ... map to step order
afl capture status WATER-LOG-ENTRY                 # coverage for a flow
```

Platforms: `web-desktop`, `web-mobile`, `ios`, `android`.
Sources: `playwright`, `xcodebuildmcp`, `droidmind`, `manual`.

See [`afl-design.md`](afl-design.md) for the full design spec (schema, parser, web UI).

## `aor` — Unified server

`aor serve` runs one HTTP server that mounts both UIs and exposes both CLI exec APIs. Use this on a shared host so any machine with `ata` / `afl` configured with a remote can proxy to it.

```bash
aor serve                             # :4400, ata UI at /, afl UI at /afl/
aor serve --port 8080 --addr 127.0.0.1
aor serve --tls-cert cert.pem --tls-key key.pem
```

Endpoints:

- `ata` UI: `/`, exec API: `/api/v1/exec`
- `afl` UI: `/afl/`, exec API: `/api/v1/afl/exec`, upload: `/api/v1/afl/upload`
- Shared SSE: `/events`

## Remote proxy

Both `ata` and `afl` can point at a remote `aor serve` — commands are serialized and executed server-side, with the client writing local state only for things that can't be proxied (`snapshot`, `restore`, `serve`).

```bash
ata remote add myserver https://host:4400 --default
afl remote add myserver https://host:4400 --default
```

Config lives at `~/.ata/config.json` and `~/.afl/config.json`.

## Layout

```
.
├── ata/            # task tracker (CLI + web + SQLite)
├── afl/            # flow & screenshot tracker (CLI + web + SQLite + flows.md parser)
├── aor/            # unified server
├── afl-design.md   # afl design spec
├── go.work         # Go workspace
└── Makefile
```
