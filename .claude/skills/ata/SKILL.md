---
name: ata
description: >
  Use ata (Agent TAsks) to manage tasks, epics, dependencies, and tags.
  Trigger when the user asks to create, list, show, edit, close, or organize tasks,
  manage epics, add dependencies or tags, or interact with the task backlog/queue.
  Also trigger on phrases like "what's ready", "show my tasks", "create a task",
  "add a dependency", "promote to epic", "clean up tasks", or "start the web UI".
argument-hint: "[subcommand] [args...]"
allowed-tools: Bash
---

# ata — Agent TAsks

`ata` is a SQLite-backed task manager at `~/.ata/ata.db`. All tasks are scoped to a **workspace** (auto-detected from git root). The `ata` binary should be on $PATH.

## Task lifecycle

```
backlog → queue → in_progress → closed
                      ↓                     ↓
                  (unclaim) → queue    (reopen) → backlog
```

**Statuses**: `backlog`, `queue`, `in_progress`, `closed`

## Core commands

### Creating tasks
```bash
ata create "TITLE"                              # defaults to backlog
ata create "TITLE" --body "details here"        # with body (markdown)
ata create "TITLE" --body-file path/to/body.md  # body from file
echo "details" | ata create "TITLE"             # body from stdin
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
ata show ID1 ID2                # show multiple tasks
echo "id1 id2" | ata show --json    # show IDs piped from stdin
ata ready                       # queue tasks with no unresolved blockers
ata ready --limit 5             # limit results
```

### Editing tasks
```bash
ata edit ID --title "New title"
ata edit ID --body "Updated body"               # tasks and epics
ata edit ID --body-file path/to/file.md         # tasks and epics
echo "updated body" | ata edit ID               # body from stdin
ata edit ID --epic EPIC_ID                      # reparent task to epic
ata edit ID --epic none                         # remove from epic
```

### Closing tasks
```bash
ata close ID                    # mark complete
ata close ID "reason text"      # with close reason
echo "id1 id2" | ata close      # bulk close from stdin
echo "id1 id2" | ata close "reason"   # bulk close with shared reason
```

### Reopening tasks
```bash
ata reopen ID                   # move closed task back to backlog
ata reopen ID1 ID2              # bulk reopen
echo "id1 id2" | ata reopen     # reopen IDs piped from stdin
```

### Claiming
```bash
ata claim ID                    # set to in_progress, store PID
ata claim ID --pid 12345        # override PID
ata unclaim ID                  # reset to queue
ata unclaim ID1 ID2             # bulk unclaim
echo "id1 id2" | ata unclaim    # unclaim IDs piped from stdin
ata unclaim --all               # unclaim all in-progress for workspace
ata recover                     # recover tasks with dead PIDs
```

## Epics

Epics are tasks with `is_epic=true`. Epics and tasks share a single markdown `body` field. `ata promote` is a pure type flip that preserves it; use `ata edit --body` / `--body-file` to update an epic's body. Child tasks inherit epic-level dependencies.

```bash
ata promote ID                          # promote task to epic (body preserved)
ata epic-close-eligible                 # list epics eligible for close (all children closed)
ata epic-close-eligible --close         # actually close eligible epics
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
echo "id1 id2" | ata tag add tag1   # bulk-tag IDs from stdin
echo "id1 id2" | ata tag rm tag1    # bulk-untag IDs from stdin
ata tag list                    # all tags in workspace
```

## Comments

```bash
ata comment ID "message"                    # default author: human
ata comment ID --body-file path/to/body.md  # body from file
echo "message" | ata comment ID             # body from stdin
ata comment ID "message" --author agent     # agent-authored
ata comment ID "message" --author system    # system-authored
ata comment edit COMMENT_ID "new body"      # edit a comment by numeric id
echo "new body" | ata comment edit COMMENT_ID
ata comment rm COMMENT_ID                   # delete a comment
```

Comment IDs are numeric and visible in `ata show ID --json` output.

## Reordering and batch moves

```bash
ata reorder ID --position 1     # set queue/backlog position
ata move --from backlog --to queue          # move all from one status to another
ata move ID1 ID2 --to queue                 # move specific tasks
echo "id1 id2" | ata move --to queue        # move IDs piped from stdin
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

htmx-powered dashboard with drag-to-reorder, inline editing, tag autocomplete, dependency editor, and live SSE updates. Also exposes `POST /api/v1/exec` for remote CLI access.

## Remote servers

Configure a workspace to proxy all CLI commands to a remote `ata serve`:

```bash
ata remote add /path/to/repo http://remote:4400         # map workspace to remote
ata remote add /local http://remote:4400 --workspace /remote  # with path remap
ata remote add myserver http://remote:4400 --default     # set default remote
ata remote list                                          # show configured remotes
ata remote remove /path/to/repo                          # remove a remote
```

Once configured, `ata` commands in that workspace transparently proxy to the remote. `snapshot`, `restore`, and `serve` always run locally.

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
ata create "User onboarding redesign" --body-file onboarding.md --status queue
ata promote ID                                   # flip to epic; body is preserved
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

## Piping IDs from stdin

`move`, `close`, `reopen`, `unclaim`, `show`, and `tag add`/`tag rm` accept
task IDs from stdin (whitespace-separated). Combine with `ata list --json | jq`
to bulk-operate on a filtered set:

```bash
ata list --status queue --tag stale --json | jq -r '.[].id' | ata move --to backlog
ata list --status in_progress --json | jq -r '.[].id' | ata unclaim
ata list --epic ABC --json | jq -r '.[].id' | ata tag add migrated
```

For `close` and `tag add`/`rm`, positional args become the reason / tag names
when IDs are supplied via stdin.

## Tips

- Task IDs are short base36 strings (e.g., `a1b`, `2xf`) — use them directly in commands.
- Workspace is auto-detected from git root; worktrees resolve to the main repo.
- `ata ready` is the best way to find unblocked work — it respects dependencies.
- Use `--json` when you need to parse output programmatically.
