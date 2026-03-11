# Architecture

## Overview

aor is a **process-level orchestrator** — it never uses the Claude API directly. It shells out to the `claude` CLI in headless mode, reads structured JSON from stdout, renders a terminal UI, and coordinates task state through the `ata` CLI. The sentinel pattern (magic prefix + JSON) is the contract between the orchestrator and the inner Claude agent for passing structured results back.

## How aor calls Claude Code

The core is in `session.go` — the `runSession` function spawns Claude Code as a child process:

```go
args := []string{
    "-p", prompt,                          // non-interactive, prompt passed as arg
    "--verbose",
    "--output-format", "stream-json",      // structured JSON on stdout
    "--max-turns", fmt.Sprintf("%d", cfg.MaxTurns),
}
if cfg.Yolo {
    args = append(args, "--dangerously-skip-permissions")
}
cmd := exec.Command("claude", args...)
```

Key flags:
- **`-p <prompt>`** — headless mode, prompt as sole input
- **`--output-format stream-json`** — one JSON object per line on stdout
- **`--dangerously-skip-permissions`** — (default on, `--no-yolo` to disable) skip tool approval prompts

## Prompt Construction

Three prompt builders, each for a different mode:

1. **Task execution** (`runner.go`, `buildPrompt`) — Tells Claude it has a pre-claimed task, instructs it to implement, commit, and close. Injects the epic spec when the task belongs to an epic. Includes workspace path and batch size for multi-task sessions.

2. **Code review** (`review_prompt.go`, `buildReviewPrompt`) — Inlines a git diff and asks Claude to find/fix issues across 6 priority areas. Used by `aor rev`.

3. **Post-task triage** (`triage.go`) — After each session, gathers evidence (commits, diff stats, task status) and either heuristically determines the outcome or spawns a triage agent to assess ambiguous results.

All prompts end with a **sentinel instruction** — a required structured JSON line the agent must output as its final action.

## Stream Processing

Claude's stdout is piped and read line-by-line. Each line is a JSON object with a `type` field:

| Type | What happens |
|------|-------------|
| `system` (subtype `init`) | Captures `session_id` |
| `assistant` | Text printed bold; tool calls rendered with gutter — Edit calls show syntax-highlighted diffs |
| `user` | Tool results — suppressed to reduce noise |
| `result` | Token usage, cost, turn count, duration |

All raw JSON is also written to a per-session log file in `~/.ata/runner-logs/`.

## Sentinel Parsing

After a session ends, `parseSentinelJSON` scans the output for a magic prefix followed by JSON:

```
ATA_RUNNER_STATUS:{"completed": ["f7q"], "discovered": ["x2k"], ...}
REVIEW_STATUS:{"fixes_applied": [...], "severity": "minor", ...}
TRIAGE_STATUS:{"outcome": "complete", "comment": "..."}
```

Two scan strategies: per-line (fast path), then concatenated text (handles streaming splits across messages).

## The Orchestration Loop

```
┌──────────────────────────────────────────┐
│  ata ready --json  →  get queue tasks    │
│         ↓                                │
│  topTask() → pick lowest sort_order      │
│         ↓                                │
│  ata claim <id>  (pre-claim with PID)    │
│         ↓                                │
│  buildPrompt() → inject spec, workspace  │
│         ↓                                │
│  runSession() → spawn `claude -p ...`    │
│    ├── stream stdout (display + log)     │
│    ├── monitor stdin (i/s/q controls)    │
│    └── handle Ctrl+C signals             │
│         ↓                                │
│  parseSentinelJSON → extract status      │
│         ↓                                │
│  If completed: update stats, close task  │
│  If not: triage → unclaim or comment     │
│         ↓                                │
│  ata epic-close-eligible → auto-close    │
│         ↓                                │
│  ata recover → reclaim dead-PID tasks    │
│         ↓                                │
│  Loop back to top (3s pause)             │
└──────────────────────────────────────────┘
```

### Stuck Task Recovery

Tasks track the PID of the aor process that claimed them (`claimed_pid` column). On each loop iteration, `ata recover` checks for in-progress tasks whose PID is no longer alive (via `kill -0`) and resets them to queue.

### Epic Auto-Close

After each task completion, `ata epic-close-eligible` finds epics where all children are closed and closes them automatically.

## Interactive Controls

A goroutine reads stdin via a shared channel. While Claude is running:

- **`i` + Enter** — Kill headless session, run `claude --resume <session_id>` interactively. On exit, the runner loop resumes.
- **`s` + Enter** — Kill session, unclaim task, move to next.
- **`q` + Enter** — Finish current session, then exit.
- **Ctrl+C** — SIGINT to Claude. Double-press within 2s force-kills.

## `aor rev` — Iterative Code Review

A separate loop in `review.go`:

1. Compute `git diff <base>...HEAD` + working tree changes
2. Spawn Claude session with review prompt
3. Parse `REVIEW_STATUS:` sentinel
4. Check convergence (no issues, all minor, repeating issues, HEAD cycling)
5. Repeat up to `--max-rounds` (default 3)
6. If uncommitted fixes remain, run a final commit sweep session

## ata — Task Management

ata is a separate Go module (`aor/ata`) linked via `go.work`. It provides:

- **SQLite storage** (`~/.ata/ata.db`, WAL mode) — no external services or sync
- **CLI** — CRUD, claim/unclaim, epic management, dependencies, tags, workspace management, recovery, all with `--json` support
- **Web UI** — htmx + SSE on `:4400` with drag-to-reorder, cross-list drag, inline editing, tag filter bar, dependency management, live updates
- **Workspace scoping** — tasks are scoped by registered workspace path, with git worktree resolution

### Data Model

```sql
-- Schema V1: Core tables
tasks:      id (base36), title, body, status, sort_order, epic_id, workspace,
            is_epic, spec, claimed_pid, claimed_at, closed_at, close_reason,
            created_at, updated_at

comments:   id, task_id, body, author (human|agent|system), created_at

-- Schema V2: Workspace registration + worktree tracking
workspaces: path (PK), created_at

tasks += worktree (where agent is currently working, transient)
tasks += created_in (where task was originally created, immutable)

-- Schema V3: Workspace naming
workspaces += name (short alias for cleaner URLs and CLI usage)

-- Schema V4: Task dependencies
task_deps:  task_id, depends_on (composite PK), created_at
            CHECK (task_id != depends_on)  -- no self-deps

-- Schema V5: Task tags
task_tags:  task_id, tag (composite PK, COLLATE NOCASE), created_at
            -- case-insensitive: "Bug" and "bug" are the same tag
            -- no registry table: SELECT DISTINCT tag FROM task_tags is the source of truth
            -- cascade-deleted when parent task is deleted
```

Task statuses: `backlog` → `queue` → `in_progress` → `closed`

IDs are 3-char base36 random strings (e.g. `f7q`), escalating to 4+ chars on collision.

### Tags

Tags are free-form, case-insensitive labels stored in the `task_tags` join table (V5 migration). Any string is a valid tag — no registration step.

Auto-coloring: the web UI assigns each tag a deterministic HSL color derived from an FNV-1a hash of the lowercased tag name. Same tag = same color everywhere.

DB layer (`db/tags.go`): `AddTag` (INSERT OR IGNORE), `RemoveTag`, `GetTags` (single task), `GetTagsForTasks` (batch load via IN clause), `ListAllTags` (distinct tags, optional workspace filter).

Filtering: `ListTasks` and `ReadyTasks` accept an optional `tag` parameter that adds `AND id IN (SELECT task_id FROM task_tags WHERE tag = ?)`. The web workspace handler derives the filter bar tags from `tagMap` when unfiltered, falling back to `ListAllTags` only when a tag filter is active (since `tagMap` reflects only the filtered subset).

### Workspace Resolution

When a command needs the current workspace, `detectWorkspace` in `cmd/util.go`:

1. Gets `git rev-parse --show-toplevel`
2. Checks if that path is a registered workspace → use it
3. Runs `git worktree list --porcelain`, takes the main worktree path
4. Checks if the main worktree is registered → use it
5. Falls back to the git toplevel

This ensures all worktrees of the same repo resolve to a single workspace.

### Dependency System

Dependencies are stored in the `task_deps` table (V4 migration). Cycle detection uses a recursive CTE in `AddDep`:

```sql
WITH RECURSIVE chain(id) AS (
    SELECT ? -- start from the would-be depended-on task
    UNION ALL
    SELECT td.depends_on FROM task_deps td JOIN chain c ON c.id = td.task_id
)
SELECT 1 FROM chain WHERE id = ? -- check if we'd reach the task itself
```

Blocked tasks (those with unclosed blockers) are excluded from `ReadyTasks` and rejected by `ClaimTask`.

### Web UI Architecture

The web server (`web/server.go`) uses Go 1.22's `http.ServeMux` with path parameters. Templates are parsed per-page (each gets layout + partials + its own template) to avoid `"content"` block conflicts.

Key patterns:
- **htmx mutations** — POST handlers detect `HX-Request` header and respond with `HX-Redirect` or rendered partials
- **SSE hub** — pub/sub broadcast for real-time updates; workspace-scoped event filtering
- **SortableJS** — drag-to-reorder with `group` option for cross-list moves (queue ↔ backlog); sends target status + position
- **Named workspace URLs** — workspaces with a name use `/w/{name}` routes; unnamed fall back to `/w?path=...`
- **Tag filter bar** — workspace view renders all tags as clickable auto-colored pills; clicking filters all status columns by that tag
- **Tag management** — task detail page has a tag editor with add/remove and `<datalist>` autocomplete from existing workspace tags

### How aor uses ata

aor shells out to the `ata` CLI binary (not a library import) via `ata.go`. This keeps the two modules loosely coupled — ata can be used standalone, and aor treats it as an external dependency in PATH.
