# aor — Agent ORchestration

A Go workspace of small, composable tools for coordinating work across agents and auditing UI flows.

## Tools

| Binary | Purpose | Data |
|--------|---------|------|
| **[`ata`](#ata--agent-tasks)** | Task tracker — backlog, queue, epics, dependencies, tags | `~/.ata/ata.db` |
| **[`atx`](#atx--agent-terminals)** | Tailscale-only PWA that mirrors remote tmux windows and routes Claude Code prompts as push notifications | `~/.atx/atx.db`, `~/.config/atx/` |
| **[`aor`](#aor--unified-server)** | Unified web server that hosts the `ata` and `atx` UIs | — |

All three are Go modules in a single workspace (`go.work`). `ata` runs locally against its own SQLite database or transparently proxies commands to a remote `aor serve` instance when a remote is configured; `atx` runs only as part of `aor serve` (it isn't a standalone CLI).

## Install

```bash
make install     # builds and installs ata, atx, aor to $GOBIN
make check       # vet + test
```

Requires Go 1.23+.

## `ata` — Agent TAsks

Task manager scoped to a workspace (auto-detected from the git root). Tasks move `backlog → queue → in_progress → closed`; epics group child tasks and share the same `body` field (markdown) used for tasks.

```bash
ata create "Refactor auth middleware" --tag backend
ata create "User onboarding redesign" --body-file plan.md
ata list                    # active tasks in this workspace
ata ready                   # queue tasks with no unresolved blockers
ata claim <id>              # take a task (sets in_progress)
ata close <id>              # mark complete
ata promote <id>            # flip task to epic (body preserved)
ata dep add <task> <blocker>           # dependency
ata serve                   # htmx web UI on :4400
```

Full command reference: `ata help`, or `.claude/skills/ata/SKILL.md`.

## `atx` — Agent terminals

A Tailscale-only PWA that mirrors every tmux window across every Tailnet host (configured in `~/.config/atx/atx.toml`) and surfaces them as one-tap-navigable web terminals on desktop and phone.

Behind the scenes:

- One persistent SSH connection per machine over Tailscale MagicDNS, with `tmux -CC` (control mode) feeding live window/session events into an SSE stream the UI subscribes to.
- Opening a window in the browser dials a second SSH session to a grouped tmux session that shares windows with the user's main session; xterm.js attaches as a real tmux client, so the pane's size follows the browser geometry and detaches when the tab is hidden.
- Claude Code hooks on each remote host (`atx/hooks/*.sh`, installed via `~/dev-vm/sync.sh` into `~/.claude/atx-hooks/`) POST every `Notification` / `Stop` event to `/atx/api/hooks/event`. atx fans them out as Web Push to subscribed PWA devices; clicks deep-link back to the originating window. Machine names come from Tailscale's MagicDNS so they match `atx.toml` regardless of what the local OS thinks the hostname is.

Configuration: copy `atx/atx.toml.example` to `~/.config/atx/atx.toml` and list each Tailnet host with a display name, color, and SSH user. atx has no CLI — it runs as part of `aor serve` and is reachable at `/atx/`.

## `aor` — Unified server

`aor serve` runs one HTTP server that mounts the `ata` and `atx` UIs and exposes the CLI exec APIs. Use this on a shared host so any machine with `ata` configured with a remote can proxy to it.

```bash
aor serve                             # :4400, ata UI at /, atx UI at /atx/
aor serve --port 8080 --addr 127.0.0.1
aor serve --tls-cert cert.pem --tls-key key.pem
```

Endpoints:

- `ata` UI: `/`, exec API: `/api/v1/exec`
- `atx` UI: `/atx/`, terminal WS: `/atx/ws`, push: `/atx/api/push/{subscribe,unsubscribe,vapid-public-key}`, hook ingest: `/atx/api/hooks/event`
- Shared SSE: `/events`

## Remote proxy

`ata` can point at a remote `aor serve` — commands are serialized and executed server-side, with the client writing local state only for things that can't be proxied (`snapshot`, `restore`, `serve`).

```bash
ata remote add myserver https://host:4400 --default
```

Config lives at `~/.ata/config.json`.

## Layout

```
.
├── ata/            # task tracker (CLI + web + SQLite)
├── atx/            # tmux web UI + Web Push + Claude Code hooks (web-only, no CLI)
├── aor/            # unified server
├── go.work         # Go workspace
└── Makefile
```
