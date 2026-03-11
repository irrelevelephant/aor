# aor + ata

**aor** (Agent Orchestration Runner) is a process-level orchestrator that drives [Claude Code](https://docs.anthropic.com/en/docs/claude-code) through a queue of tasks. **ata** (Agent Task Automator) is the SQLite-backed task manager it pulls work from.

Together they form a loop: you create and prioritize tasks in ata (via CLI or web UI), then aor works through them autonomously — claiming tasks, spawning Claude Code sessions, streaming output, and closing tasks on completion.

## Getting Started

### Prerequisites

- Go 1.22+
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) CLI (`claude`) in PATH

### Install

```sh
# Clone the repo
git clone <repo-url> aor && cd aor

# Install both binaries
go install .            # installs aor
cd ata && go install .  # installs ata
```

### Quick Start

**1. Register a workspace and create some tasks:**

```sh
cd your-project/
ata init --name myproject
ata create "Add input validation to /api/users endpoint"
ata create "Fix race condition in cache layer" --tag backend,urgent
ata create "Write tests for auth middleware" --tag testing
```

New tasks go to the **queue** by default. Use `--status backlog` to park ideas for later. Tags are free-form labels — any string works, created on first use.

**2. Organize with tags and dependencies:**

```sh
ata tag add <id> backend           # add tags after creation
ata dep add <task> <blocker>       # task won't run until blocker is closed
ata list --tag backend             # filter by tag
```

**3. Run the orchestrator:**

```sh
aor
```

aor will claim the top task, spawn a Claude Code session, stream its work to your terminal, and move on to the next task when done.

**4. Or manage tasks visually:**

```sh
ata serve
# Open http://localhost:4400
```

The web UI lets you drag-to-reorder (including across queue/backlog), filter by tag, create/edit tasks, manage dependencies and tags, view epics, and watch agent progress via live SSE updates.

### Interactive Controls

While aor is running:

| Key | Action |
|-----|--------|
| `i` + Enter | Interject — drop into interactive Claude, then resume the loop |
| `s` + Enter | Skip current task |
| `q` + Enter | Quit after current task finishes |
| Ctrl+C | Stop agent and exit |
| Ctrl+C x2 | Force kill immediately |

## How It Works

```
you (web UI / CLI)          aor (orchestrator)            claude (agent)
─────────────────           ──────────────────            ──────────────
create tasks ──────────►  ata ready --json
prioritize queue            pick top task
                            ata claim <id>
                            build prompt ─────────────►  claude -p "..."
                            stream stdout ◄────────────  JSON lines
                            parse sentinel ◄───────────  ATA_RUNNER_STATUS:{...}
                            ata close <id>
                            loop ──────────────────────►  next task...
```

### Task Lifecycle

```
backlog ──► queue ──► in_progress ──► closed
   │                      │
   └──────────────────────┘  (unclaim resets to queue)
```

- **backlog** — raw ideas, not yet prioritized
- **queue** — prioritized, ready for an agent to pick up (default for new tasks)
- **in_progress** — claimed by an aor session (tracked by PID)
- **closed** — completed with a reason

### Tags

Tasks can have free-form tags for categorization. Tags are case-insensitive and created on first use — no registration step.

```sh
ata tag add <task> backend urgent   # add one or more tags
ata tag rm <task> urgent            # remove a tag
ata tag list                        # list all tags in use
ata create "New task" --tag api,backend  # tag at creation time
ata list --tag backend              # filter tasks by tag
ata ready --tag backend             # filter ready tasks by tag
```

In the web UI, tags appear as auto-colored badges on tasks. The workspace view has a tag filter bar — click a tag to filter, click "all" to clear. The task detail page has a tag editor with autocomplete from existing tags.

### Dependencies

Tasks can declare "blocked by" relationships. A blocked task won't appear in `ata ready` and can't be claimed until all its blockers are closed.

```sh
ata dep add <task> <blocker>    # task is blocked by blocker
ata dep rm <task> <blocker>     # remove the dependency
ata dep list <task>             # show blockers and blocking relationships
```

Cycle detection prevents circular dependencies. The web UI shows blocked badges on tasks and provides an interactive dependency editor on the task detail page.

### Epics

Any task can be promoted to an epic. Epics have a markdown **spec** (goals, constraints, architecture) that gets injected into the agent's prompt for all child tasks. When all children are closed, the epic auto-closes.

```sh
ata create "Rewrite auth system"
ata promote <id> --spec-file auth-spec.md
ata create "Migrate session tokens" --epic <id>
ata create "Add OAuth2 provider" --epic <id>
```

### Pulling Tasks

`ata pull <id>` claims a task and launches an interactive Claude Code session with a structured workflow:

1. **Research & Plan** — Claude explores the codebase and writes a concrete plan
2. **Review** — Claude presents the plan and asks you to choose:
   - **Request changes** — refine the plan before acting
   - **Execute directly** — Claude implements the plan in the current session, then offers to resolve the task
   - **Decompose** — Claude creates subtasks (with `--epic`, dependencies, and ordering), then exits so you can run `aor --epic <id>` for autonomous execution

### Workspaces

Tasks are scoped to a **workspace** — typically a git repository root, auto-detected from `git rev-parse --show-toplevel`. Different repos have independent task queues.

Register a workspace with a short name to get cleaner URLs and easier CLI usage:

```sh
ata init --name myproject     # register current repo with a short name
ata clean --workspace myproject --force  # delete all tasks and unregister
```

Workspaces resolve correctly across git worktrees — all worktrees of the same repo map to the registered workspace path.

## ata CLI Reference

```
ata init [--workspace PATH] [--name NAME]                  Register workspace
ata clean [--workspace NAME|PATH] [--force]                Delete all workspace data
ata create TITLE [--body TEXT] [--status backlog|queue] [--epic ID] [--tag a,b] [--workspace PATH] [--json]
ata list [--workspace PATH] [--status STATUS] [--epic ID] [--tag TAG] [--all] [--json]
ata show ID [--json]
ata close ID [REASON] [--json]
ata ready [--workspace PATH] [--epic ID] [--tag TAG] [--limit N] [--json]
ata claim ID [--json]
ata unclaim [ID] [--workspace PATH] [--json]
ata pull ID                                                Plan, review, and execute/decompose with Claude
ata promote ID [--spec-file PATH]                          Promote task to epic
ata spec ID [--set-file PATH] [--json]
ata comment ID BODY [--author human|agent|system] [--json]
ata dep add TASK DEPENDS_ON                                Add dependency
ata dep rm TASK DEPENDS_ON                                 Remove dependency
ata dep list TASK                                          Show dependencies
ata tag add TASK TAG [TAG...]                              Add tags
ata tag rm TASK TAG [TAG...]                               Remove tags
ata tag list [--workspace WS] [--json]                     List all tags in use
ata reorder ID --position N
ata recover [--workspace PATH] [--json]
ata epic-close-eligible [--workspace PATH] [--json]
ata serve [--port 4400] [--addr 0.0.0.0]
```

All mutation commands support `--json` for structured output, making ata scriptable.

## aor CLI Reference

```
aor [flags]              Run the task orchestration loop
aor rev [flags] [<ref>]  Iterative code review
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--epic ID` | | Only work on tasks under this epic |
| `--max-tasks N` | 0 (unlimited) | Stop after N tasks |
| `--batch-size N` | 1 | Tasks per Claude session before fresh context |
| `--max-turns N` | 150 | Max agent turns per session |
| `--dry-run` | false | Show what would happen without running |
| `--supervised` | false | Approve each task before running |
| `--unclaim` | false | Reset all in-progress tasks to queue and exit |
| `--no-yolo` | false | Require permission prompts (default: skip) |
| `--workspace PATH` | auto-detect | Workspace path |
| `--log-dir PATH` | `~/.ata/runner-logs` | Session log directory |

### `aor rev` — Iterative Code Review

Runs Claude Code in a review loop against your recent changes:

```sh
aor rev              # review changes since merge-base with main
aor rev HEAD~3       # review last 3 commits
aor rev --max-rounds 5
```

Each round files tasks for issues found and applies fixes directly. Stops when no issues remain or the round limit is reached.

## Web UI

`ata serve` starts an htmx-powered web interface on `:4400`.

- **Dashboard** — lists all registered workspaces with active task counts; auto-redirects when only one workspace exists
- **Workspace view** — two-column layout (queue + backlog) with drag-to-reorder and cross-list drag; in-progress section below; tag filter bar for quick filtering by tag
- **Task detail** — inline-editable title/body, markdown rendering, tag editor with autocomplete, comment thread, dependency editor (add/remove blockers, view blocking relationships)
- **Epic view** — rendered spec, child task list with progress bar and tag badges, back-link to workspace
- **Named URLs** — workspaces with short names use `/w/name` instead of `/w?path=...`
- **Live updates** — SSE pushes changes from CLI/agent activity in real time

## Project Structure

```
aor/
  main.go              CLI entry, flag dispatch
  runner.go            Orchestration loop, prompt builder
  session.go           Claude Code process management, stream parsing
  review.go            Iterative code review (aor rev)
  triage.go            Post-session triage (heuristic + agent)
  ata.go               ata CLI wrapper functions
  types.go             Shared types
  git.go               Git helpers (worktree detection, diff)
  logger.go            Session logging
  highlight.go         Syntax-highlighted terminal output

  ata/                 Task management module
    main.go            CLI entry
    model/             Types (Task, Comment, Workspace, EpicProgress)
    db/                SQLite layer (WAL mode, migrations V1-V5, CRUD)
      migrations.go    Schema versions (tasks, comments, workspaces, task_deps, task_tags)
      tasks.go         Task CRUD, claiming, recovery
      workspaces.go    Workspace registration, resolution, cleanup
      deps.go          Dependency CRUD, cycle detection
      tags.go          Tag CRUD, batch loading
      comments.go      Comment CRUD
      ordering.go      Sort order management
    cmd/               CLI command implementations
    web/               HTTP server, templates, SSE, static assets
```

## Data Storage

All data lives in `~/.ata/ata.db` (SQLite, WAL mode). Session logs go to `~/.ata/runner-logs/`.

No external services, no sync, no git-backed storage. The database is a single file you can back up, inspect with `sqlite3`, or delete to start fresh.
