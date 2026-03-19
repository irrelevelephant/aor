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

Six prompt builders, each for a different mode:

1. **Task execution** (`runner.go`, `buildPrompt`) — Tells Claude it has a pre-claimed task, instructs it to implement, commit, and close. Injects the epic spec when the task belongs to an epic. Includes workspace path and batch size for multi-task sessions.

2. **Interactive pull** (`pull_prompt.go`, `buildPullPrompt`) — Multi-phase workflow for interactive task work: research, plan, review with user (via AskUserQuestion), then execute directly or decompose into subtasks. Includes a GSD-style interview phase for underspecified tasks (depth: full, light, or skip).

3. **Worktree merge** (`merge_prompt.go`, `buildMergePrompt`) — Instructs Claude to analyze worktree branches, decide merge order, merge into the main branch, resolve conflicts, and clean up merged worktrees.

4. **Code review** (`review_prompt.go`, `buildReviewPrompt`) — Inlines a git diff and asks Claude to find/fix issues across 6 priority areas. Tasks are created with a `--tag` flag so sweep mode can scope orchestration to just this session's work. Used by `aor rev`.

5. **Spec planning** (`spec_prompt.go`, `buildSpecPrompt`) — Three-phase interactive workflow for spec-driven planning: research the codebase, refine the spec, then decompose into epics and tasks with dependencies. Supports single or multi-spec sessions (multi-spec adds cross-epic dependency handling). Used by `aor spec`.

6. **Post-task triage** (`triage.go`) — After each session, gathers evidence (commits, diff stats, task status) and either heuristically determines the outcome or spawns a triage agent to assess ambiguous results.

Task execution, code review, and triage prompts end with a **sentinel instruction** — a required structured JSON line the agent must output as its final action. Pull, merge, and spec sessions are interactive (stdin/stdout piped directly) and don't use sentinels.

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
│  epic close check → verify or auto-close  │
│         ↓                                │
│  ata recover → reclaim dead-PID tasks    │
│         ↓                                │
│  Loop back to top (3s pause)             │
└──────────────────────────────────────────┘
```

### Stuck Task Recovery

Tasks track the PID of the aor process that claimed them (`claimed_pid` column). On each loop iteration, `ata recover` checks for in-progress tasks whose PID is no longer alive (via `kill -0`) and resets them to queue.

### Epic Close & Verification

After each task completion, `ata epic-close-eligible` finds epics where all children are closed. Epics **without** a spec auto-close immediately. Epics **with** a spec enter a verification loop (`verify.go`):

1. A verification agent examines the codebase against the epic's acceptance criteria
2. If all criteria pass → close the epic
3. If criteria fail → the agent files new tasks for gaps, the orchestrator works them, then re-verifies
4. Loop up to `--max-rounds` (default 3) times

When the runner's queue is empty and `--epic` is set, it also checks whether the filtered epic is eligible for verification (open, has a spec, all children closed) and runs the verification loop directly. The inner orchestration run sets `SkipEpicClose` to prevent recursive verification.

Epics are excluded from `ReadyTasks` (`AND is_epic = 0`) so they never appear in the work queue.

## Interactive Controls

A goroutine reads stdin via a shared channel. While Claude is running:

- **`i` + Enter** — Kill headless session, run `claude --resume <session_id>` interactively. On exit, the runner loop resumes.
- **`s` + Enter** — Kill session, unclaim task, move to next.
- **`q` + Enter** — Finish current session, then exit.
- **Ctrl+C** — SIGINT to Claude. Double-press within 2s force-kills.

## `aor pull` — Interactive Task Work

`aor pull` claims a task, creates a git worktree (by default), and launches an interactive Claude Code session. The session uses AskUserQuestion for a multi-phase workflow:

1. **Research & Plan** — Claude explores the codebase, writes a concrete plan
2. **Review** — presents the plan and asks the user to choose: execute, decompose, or revise
3. **Execute** — implements, tests, commits, runs /simplify, closes the task
4. **Decompose** — creates subtasks with dependencies under the epic, exits for autonomous execution

If no task ID is given, a bubbletea-based fuzzy selector shows ready tasks.

## `aor merge` — Worktree Merge

`aor merge` discovers all git worktrees, gathers their commit histories, and launches a single interactive Claude Code session to merge them into the main branch. Claude decides merge order, resolves conflicts autonomously (asking the user only for genuinely ambiguous cases), and cleans up merged worktrees.

Supports `--exclude` to skip specific worktrees and positional args to include only specific ones.

## `aor rev` — Iterative Code Review with Sweep Mode

Two nested loops in `review.go`:

**Inner loop** (review rounds, managed by `revContext.runReviewCycle`):

1. Compute `git diff <base>...HEAD` + working tree changes
2. Spawn Claude session with review prompt (tasks tagged `rev-<worktree-basename>`)
3. Parse `REVIEW_STATUS:` sentinel
4. Safety-net: ensure all filed tasks have the rev tag via `ata tag add`
5. Check convergence (no issues, all minor, repeating issues, HEAD cycling)
6. Repeat up to `--max-rounds` (default 3)

**Outer loop** (sweep cycles, in `runRevDirect`):

1. Run inner review loop
2. Commit sweep — catch uncommitted review fixes
3. Check for open tagged tasks via `ata ready --tag rev-<name>`
4. If none remain → done (clean pass)
5. Run orchestration loop (`run()`) filtered to the rev tag — fixes the filed tasks
6. Loop back for another review pass

The outer loop has no hard cycle cap — convergence checks in the inner loop and the "no open tasks" check provide the safety net. The `revContext` struct holds stable state (config, base ref, tag, logger, stdin channel) shared across sweep cycles.

The review logic lives in `runRevDirect()`, which accepts a pre-initialized logger and stdin channel. The `runRev()` entry point is a thin wrapper that parses flags and creates these resources. This split allows `runMultiEpic()` to call `runRevDirect()` inline with shared resources (avoiding dual stdin reader problems).

## Multi-Epic Processing

`runMultiEpic()` in `main.go` processes multiple epics serially. Comma-separated `-epic` values are collected by `collectEpics()` and looped over:

1. Record pre-epic HEAD SHA
2. Run the orchestration loop (`run()`) for this epic
3. If `--rev` is set and HEAD advanced, run `runRevDirect()` with the pre-epic SHA as base
4. Continue to the next epic

Shared resources (logger, stdin channel, `RunStats`) are created once and passed to each `run()` call via Config fields. Each iteration gets a shallow copy of the Config to avoid mutating the original. Review failures don't stop the run — they're logged and processing continues to the next epic.

## ata — Task Management

ata is a separate Go module (`aor/ata`) linked via `go.work`. It provides:

- **SQLite storage** (`~/.ata/ata.db`, WAL mode) — no external services or sync
- **CLI** — CRUD, claim/unclaim, epic management, dependencies, tags, workspace management, recovery, all with `--json` support (orchestration commands like pull and merge live in aor)
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

Blocked tasks (those with unclosed blockers) and epics (`is_epic = 0`) are excluded from `ReadyTasks`. Blocked tasks are also rejected by `ClaimTask`.

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
