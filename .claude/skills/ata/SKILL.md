---
name: ata
description: >
  Use ata (Agent TAsks) to manage tasks, epics, dependencies, and tags.
  Trigger when the user asks to create, list, show, edit, close, or organize tasks,
  manage epics or specs, add dependencies or tags, or interact with the task backlog/queue.
  Also trigger on phrases like "what's ready", "show my tasks", "create a task",
  "add a dependency", "promote to epic", "clean up tasks", or "start the web UI".
argument-hint: "[subcommand] [args...]"
allowed-tools: Bash
---

# ata — Agent TAsks

`ata` is a SQLite-backed task manager at `~/.ata/ata.db`. All tasks are scoped to a **workspace** (auto-detected from git root). The CLI binary is at `/home/tjs/aor/ata/`.

## Task lifecycle

```
backlog → queue → in_progress → closed
                      ↓
                  (unclaim) → queue
```

**Statuses**: `backlog`, `queue`, `in_progress`, `closed`

## Core commands

### Creating tasks
```bash
ata create "TITLE"                              # defaults to backlog
ata create "TITLE" --description "details here" # with description (--desc alias)
ata create "TITLE" --status queue               # directly to queue
ata create "TITLE" --epic EPIC_ID               # under an epic
ata create "TITLE" --tag backend,urgent         # with tags
```

### Listing and viewing
```bash
ata list                        # active tasks (excludes closed)
ata list --all                  # include closed
ata list --status queue         # filter by status
ata list --epic EPIC_ID         # tasks under an epic
ata list --tag backend          # filter by tag
ata show ID                     # full task details
ata ready                       # queue tasks with no unresolved blockers
ata ready --limit 5             # limit results
```

### Editing tasks
```bash
ata edit ID --title "New title"
ata edit ID --description "Updated desc"        # tasks only (--desc alias)
ata edit ID --desc-file path/to/file.md         # tasks only
ata edit ID --spec "Epic spec content"          # epics only
ata edit ID --spec-file path/to/spec.md         # epics only
```

### Closing tasks
```bash
ata close ID                    # mark complete
ata close ID "reason text"      # with close reason
```

### Claiming (used by aor orchestrator)
```bash
ata claim ID                    # set to in_progress, store PID
ata claim ID --pid 12345        # override PID
ata unclaim ID                  # reset to queue
ata unclaim                     # unclaim all in-progress for workspace
ata recover                     # recover tasks with dead PIDs
```

## Epics and specs

Epics are tasks with `is_epic=true`. Epics have **specs** (structured requirements); regular tasks have **descriptions** (lightweight context). The `ata spec` command is epic-only.

```bash
ata promote ID                          # promote task to epic
ata promote ID --spec-file arch.md      # promote with spec
ata spec ID                             # view epic spec (epic-only)
ata spec ID --set-file spec.md          # set epic spec (epic-only)
ata epic-close-eligible                 # auto-close epics with all children closed
```

## Dependencies

```bash
ata dep add TASK DEPENDS_ON     # TASK is blocked by DEPENDS_ON
ata dep rm TASK DEPENDS_ON      # remove dependency
ata dep list TASK               # show blockers and what task blocks
```

Circular dependencies are rejected. Blocked tasks are excluded from `ata ready`.

## Tags

Free-form, case-insensitive labels.

```bash
ata tag add TASK tag1 tag2      # add tags
ata tag rm TASK tag1            # remove tag
ata tag list                    # all tags in workspace
```

## Comments

```bash
ata comment ID "message"                    # default author: human
ata comment ID "message" --author agent     # agent-authored
ata comment ID "message" --author system    # system-authored
```

## Reordering and batch moves

```bash
ata reorder ID --position 1     # set queue/backlog position
ata move --from backlog --to queue          # move all from one status to another
ata move ID1 ID2 --to queue                 # move specific tasks
```

## Workspace management

```bash
ata init                        # register current git root
ata init --name myproject       # register with a name
ata uninit myproject            # unregister (prompts with task counts)
ata uninit myproject --clean    # unregister and delete all tasks
ata uninit --force --clean      # skip confirmation, auto-detect workspace
```

## Cleanup

```bash
ata clean                               # delete ALL tasks (prompts for confirm)
ata clean --closed                      # delete only closed tasks
ata clean --closed --older-than 30d     # closed tasks older than 30 days
ata clean --force                       # skip confirmation
```

## Backup and restore

```bash
ata snapshot                            # export workspace to .tar.gz
ata snapshot --output backup.tar.gz     # custom output path
ata restore backup.tar.gz              # import from snapshot
ata restore backup.tar.gz --force      # skip confirmation
```

## Web UI

```bash
ata serve                       # start at :4400
ata serve --port 8080           # custom port
ata serve --addr 0.0.0.0       # bind to all interfaces
```

htmx-powered dashboard with drag-to-reorder, inline editing, tag autocomplete, dependency editor, and live SSE updates.

## JSON output

All mutation commands support `--json` for structured output:
```bash
ata create "task" --json        # returns JSON with task ID
ata list --json                 # JSON array of tasks
ata show ID --json              # JSON task object
```

## Common workflows

**Triage new work:**
```bash
ata create "Investigate flaky test in CI" --status queue --tag ci,testing
ata create "Refactor auth middleware" --tag backend
```

**Plan an epic:**
```bash
ata create "User onboarding redesign" --status queue
ata promote ID --spec-file onboarding-spec.md
ata create "Design new welcome flow" --epic ID --status queue
ata create "Implement email verification" --epic ID --status queue
ata dep add EMAIL_TASK WELCOME_TASK   # email depends on welcome flow
```

**Check what to work on:**
```bash
ata ready
```

**Review progress:**
```bash
ata list --status in_progress
ata list --epic EPIC_ID
```

## Tips

- Task IDs are short base36 strings (e.g., `a1b`, `2xf`) — use them directly in commands.
- Workspace is auto-detected from git root; worktrees resolve to the main repo.
- `ata ready` is the best way to find unblocked work — it respects dependencies.
- Use `--json` when you need to parse output programmatically.
