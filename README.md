# aor + ata

**aor** (Agent ORchestration) is a process-level orchestrator that drives [Claude Code](https://docs.anthropic.com/en/docs/claude-code) through a queue of tasks. **ata** (Agent TAsks) is the SQLite-backed task manager it pulls work from.

Together they form a loop: you create and prioritize tasks in ata (via CLI or web UI), then aor works through them autonomously — claiming tasks, spawning Claude Code sessions, streaming output, and closing tasks on completion.

## Getting Started

### Prerequisites

- Go 1.24+
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) CLI (`claude`) in PATH

### Install

```sh
# Clone the repo
git clone <repo-url> aor && cd aor

# Install both binaries
make install
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

### Standard Workflow

The typical workflow for working on a task interactively:

```
ata create  →  aor pull  →  (work)  →  aor rev  →  aor merge
```

**1. Create a task:**

```sh
ata create "Add rate limiting to API endpoints" --tag backend
```

**2. Pull the task to start working:**

```sh
aor pull
```

This opens an interactive selector showing all ready tasks. Pick one, and aor will:
- Create a git worktree at `../<repo>-<task-id>` on branch `task/<task-id>`
- Claim the task (status becomes `in_progress`)
- Launch an interactive Claude Code session with a structured planning workflow

Claude researches the codebase, presents a plan, and asks how to proceed. You can execute the plan directly, request changes, or decompose it into subtasks for autonomous execution.

**3. Review the changes:**

```sh
aor rev
```

After the pull session completes, run `aor rev` from the worktree to get an automated code review. Claude iterates over your diff, finds issues, and applies fixes directly. Issues too large to fix inline are filed as tasks and worked through automatically via the orchestration loop, then reviewed again — repeating until the code is clean.

**4. Merge back to main:**

```sh
aor merge
```

This discovers all worktrees, analyzes their branches, merges them into main (resolving conflicts automatically), and cleans up the worktrees.

For larger projects, you can also skip the interactive workflow and run `aor` to process the entire queue autonomously.

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
you (web UI / CLI)        aor (orchestrator)          claude (agent)
─────────────────         ──────────────────          ──────────────
create tasks ────────►    ata ready --json
prioritize queue          pick top task
                          ata claim <id>
                          build prompt ───────────►   claude -p "..."
                          stream stdout ◄──────────   JSON lines
                          parse sentinel ◄─────────   ATA_RUNNER_STATUS:{...}
                          ata close <id>
                          loop ────────────────────►  next task...
```

### Task Lifecycle

```
backlog ──► queue ──► in_progress ──► closed
                          │
                          └──► queue  (unclaim resets to queue)
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

`aor pull` is the primary way to work on a task interactively. It claims a task, creates an isolated git worktree, and launches a Claude Code session with a structured planning workflow.

```sh
aor pull           # interactive selector — fuzzy search through ready tasks
aor pull f7q       # pull a specific task by ID
aor pull --no-worktree f7q  # work in the current tree instead of a worktree
aor pull --no-yolo f7q      # require permission prompts for each tool call
```

**What happens when you pull:**

1. A git worktree is created at `../<repo-name>-<task-id>` on branch `task/<task-id>` (skip with `--no-worktree`)
2. The task is claimed — its status moves to `in_progress`
3. If the task belongs to an epic, the epic spec is fetched and injected into the prompt
4. An interactive Claude Code session starts in the worktree directory

**The session follows a phased workflow:**

1. **Research & Plan** — Claude explores the codebase and writes a concrete plan with specific files and changes
2. **Review with you** — Claude presents the plan and prompts you to choose:
   - **Execute** — go to phase 3a
   - **Decompose** — go to phase 3b
   - **Anything else** — treated as feedback; Claude revises the plan and asks again
3. **Execute (3a)** — Claude implements the plan, runs tests, commits changes, and runs `/simplify` for a code quality check. Then asks if you want to resolve the task. If yes, it runs `ata close <id> "done"`.
4. **Decompose (3b)** — Claude breaks the work into subtasks, creating each with `ata create --epic <id>`, adding dependencies between them, and ordering by priority. After you confirm the breakdown, exit and run `aor --epic <id>` to execute the subtasks autonomously.

**After the session exits**, aor checks the task status and tells you what to do next:

- If the task was closed: done. Run `aor rev` to review, then `aor merge` to merge the worktree.
- If the task was promoted to an epic: run `aor --epic <id>` to orchestrate the subtasks.
- If the task is still in progress: the session ended early — resume with `claude --resume` or run `aor pull <id>` again.

### Merging Worktrees

After completing work in a worktree (via `aor pull` or the autonomous loop), use `aor merge` to bring the changes back to the main branch:

```sh
aor merge                          # merge all worktrees
aor merge myproject-f7q            # merge specific worktree(s) by name
aor merge --exclude myproject-x2k  # skip specific worktree(s)
```

Claude analyzes each branch, decides the optimal merge order, resolves conflicts automatically (asking for help only when genuinely ambiguous), and cleans up successfully merged worktrees. This is the final step in the `pull → rev → merge` cycle.

### Workspaces

Tasks are scoped to a **workspace** — typically a git repository root, auto-detected from `git rev-parse --show-toplevel`. Different repos have independent task queues.

Register a workspace with a short name to get cleaner URLs and easier CLI usage:

```sh
ata init --name myproject     # register current repo with a short name
ata clean --workspace myproject --force  # delete all tasks and unregister
```

Workspaces resolve correctly across git worktrees — all worktrees of the same repo map to the registered workspace path.

Back up or transfer a workspace with snapshot/restore:

```sh
ata snapshot --workspace myproject           # creates ata-snapshot-myproject-20260311.tar.gz
ata restore ata-snapshot-myproject-20260311.tar.gz --workspace /other/path --force
```

The archive is a portable `.tar.gz` containing JSONL files — human-inspectable and decoupled from SQLite internals. Restoring replaces the target workspace entirely.

## ata CLI Reference

```
ata init [--workspace PATH] [--name NAME]                  Register workspace
ata clean [--workspace NAME|PATH] [--force]                Delete all workspace data
ata create TITLE [--body TEXT] [--status backlog|queue] [--epic ID] [--tag a,b] [--workspace PATH] [--json]
ata list [--workspace PATH] [--status STATUS] [--epic ID] [--tag TAG] [--all] [--json]
ata show ID [--json]
ata edit ID [--title TITLE] [--body BODY] [--body-file PATH] [--spec SPEC] [--spec-file PATH] [--json]
ata close ID [REASON] [--json]
ata ready [--workspace PATH] [--epic ID] [--tag TAG] [--limit N] [--json]
ata claim ID [--json]
ata unclaim [ID] [--workspace PATH] [--json]
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
ata move --from STATUS --to STATUS [--workspace WS] [--json]
ata move ID [ID...] --to STATUS [--json]
ata recover [--workspace PATH] [--json]
ata epic-close-eligible [--workspace PATH] [--json]
ata snapshot [--workspace WS] [--output FILE] [--json]  Export workspace to .tar.gz
ata restore FILE [--workspace WS] [--force] [--json]    Import workspace from snapshot
ata serve [--port 4400] [--addr 0.0.0.0] [--tls-cert FILE] [--tls-key FILE]
```

All mutation commands support `--json` for structured output, making ata scriptable.

## aor CLI Reference

```
aor [flags]                    Run the task orchestration loop
aor pull [flags] [TASK_ID]     Interactive task planning and execution
aor merge [flags] [WORKTREE…]  Merge worktrees back into main branch
aor rev [flags] [<ref>]        Iterative code review with grind mode
aor spec [flags] <file.md…>   Spec-driven task planning and execution
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--epic ID` | | Only work on tasks under this epic |
| `--tag TAG` | | Only work on tasks with this tag |
| `--max-tasks N` | 0 (unlimited) | Stop after N tasks |
| `--batch-size N` | 1 | Tasks per Claude session before fresh context |
| `--dry-run` | false | Show what would happen without running |
| `--supervised` | false | Approve each task before running |
| `--unclaim` | false | Reset all in-progress tasks to queue and exit |
| `--no-yolo` | false | Require permission prompts (default: skip) |
| `--workspace PATH` | auto-detect | Workspace path |
| `--log-dir PATH` | `~/.ata/runner-logs` | Session log directory |

### `aor rev` — Iterative Code Review with Grind Mode

Runs Claude Code in an automated review loop against your recent changes. Best used from a worktree after `aor pull` completes, before merging back to main.

```sh
aor rev              # review changes since merge-base with main
aor rev HEAD~3       # review last 3 commits
aor rev --max-rounds 5
aor rev --workspace /path/to/project
```

Each round, Claude examines the diff, files tasks for issues found, and applies fixes directly — committing as it goes. The inner review loop stops when no issues remain, all issues are minor, the round limit is reached (default: 3 rounds), or issues start repeating.

**Grind mode** (always on): after the review loop converges, if tasks were filed that are too large to fix inline, aor automatically runs the orchestration loop to work through them — then reviews again. This outer loop repeats until a review pass comes back clean. Tasks filed during review are tagged `rev-<worktree-basename>` to scope the orchestration to just this session's work. Convergence checks (no issues, minor severity, repeating issues, HEAD cycling) provide the safety net.

| Flag | Default | Description |
|------|---------|-------------|
| `--max-rounds N` | 3 | Maximum review rounds per grind cycle |
| `--no-yolo` | false | Require permission prompts |
| `--workspace PATH` | auto-detect | Workspace path |
| `--log-dir PATH` | `~/.ata/runner-logs` | Session log directory |

This is the middle step of the standard `pull → rev → merge` workflow.

## Web UI

`ata serve` starts an htmx-powered web interface on `:4400`.

- **Dashboard** — lists all registered workspaces with active task counts; auto-redirects when only one workspace exists
- **Workspace view** — two-column layout (queue + backlog) with drag-to-reorder and cross-list drag; in-progress section below; tag filter bar for quick filtering by tag
- **Task detail** — inline-editable title/body, markdown spec with edit toggle, tag editor with autocomplete, comment thread, dependency editor (add/remove blockers, view blocking relationships)
- **Epic view** — rendered spec with edit toggle, child task list with progress bar and tag badges, back-link to workspace
- **Named URLs** — workspaces with short names use `/w/name` instead of `/w?path=...`
- **Live updates** — SSE pushes changes from CLI/agent activity in real time

## Project Structure

```
aor/
  main.go              CLI entry, flag dispatch
  runner.go            Orchestration loop, prompt builder
  session.go           Claude Code process management, stream parsing
  pull.go              Interactive task planning (aor pull)
  pull_prompt.go       Pull session prompt builder
  merge.go             Worktree merge orchestration (aor merge)
  merge_prompt.go      Merge session prompt builder
  selector.go          Interactive fuzzy task selector (bubbletea)
  review.go            Iterative code review with grind mode (aor rev)
  review_prompt.go     Review session prompt builder
  spec.go              Spec planning orchestration (aor spec)
  spec_prompt.go       Spec session prompt builder
  triage.go            Post-session triage (heuristic + agent)
  ata.go               ata CLI wrapper functions
  types.go             Shared types
  git.go               Git helpers (worktree management, diff)
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
      snapshot.go      Workspace export/import
    cmd/               CLI command implementations
    web/               HTTP server, templates, SSE, static assets
```

## Development

```sh
make check    # run go vet + go test across both modules
make test     # run tests only
make vet      # run go vet only
make fmt      # list unformatted files
```

## Data Storage

All data lives in `~/.ata/ata.db` (SQLite, WAL mode). Session logs go to `~/.ata/runner-logs/`.

No external services, no sync, no git-backed storage. The database is a single file you can back up, inspect with `sqlite3`, or delete to start fresh. Use `ata snapshot` / `ata restore` for portable workspace-level backups.
