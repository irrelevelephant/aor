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

`ata` is a SQLite-backed task manager at `~/.ata/ata.db`. The DB is global — every task is visible from every directory. Each task records `created_in` (the git toplevel where it was created) for reference, but commands don't filter by it. The `ata` binary should be on $PATH.

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
ata create "TITLE"                              # defaults to queue (or epic's status if --epic)
ata create "TITLE" --body "details here"        # with body (markdown)
ata create "TITLE" --body-file path/to/body.md  # body from file
echo "details" | ata create "TITLE"             # body from stdin
ata create "TITLE" --status backlog             # explicit backlog
ata create "TITLE" --epic EPIC_ID               # under an epic (inherits epic status)
ata create "TITLE" --tag backend,urgent         # with tags (comma-separated)
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
ata ready --epic EPIC_ID        # ready tasks under an epic
ata ready --tag backend         # ready tasks with a tag
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

Closing an epic with open subtasks errors via the CLI. The web UI prompts to cascade-close instead.

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
ata claim ID --host myhost      # override hostname
ata unclaim ID                  # reset to queue
ata unclaim ID1 ID2             # bulk unclaim
echo "id1 id2" | ata unclaim    # unclaim IDs piped from stdin
ata unclaim --all               # unclaim every in-progress task
ata recover                     # recover tasks whose PIDs are dead
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
ata dep propagate SOURCE NEW    # copy SOURCE's dependents to also depend on NEW
```

Circular dependencies are rejected. Blocked tasks are excluded from `ata ready`.

## Tags

Free-form, case-insensitive labels.

```bash
ata tag add TASK tag1 tag2      # add tags
ata tag rm TASK tag1            # remove tag
echo "id1 id2" | ata tag add tag1   # bulk-tag IDs from stdin
echo "id1 id2" | ata tag rm tag1    # bulk-untag IDs from stdin
ata tag list                    # all tags in use
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
ata reorder ID --position 1     # set queue/backlog position (0-based)
ata reorder ID --top            # move to top
ata reorder ID --bottom         # move to bottom
ata reorder ID --before OTHER   # place before OTHER
ata reorder ID --after OTHER    # place after OTHER
ata reorder ID --status queue   # also change status (top-level tasks only)
ata move --from backlog --to queue          # move all from one status to another
ata move ID1 ID2 --to queue                 # move specific tasks
echo "id1 id2" | ata move --to queue        # move IDs piped from stdin
```

Moving an epic moves its entire subtree.

## Cleanup

```bash
ata clean                               # delete closed tasks (default)
ata clean --older-than 30d              # delete closed tasks older than 30 days
ata clean --all                         # delete EVERY task (prompts "yes")
ata clean --force                       # skip confirmation prompt
```

## Backup and restore

```bash
ata snapshot                            # export full DB to ata-snapshot-DATE.tar.gz
ata snapshot --output backup.tar.gz     # custom output path
ata restore backup.tar.gz               # import from snapshot (replaces existing)
ata restore backup.tar.gz --force       # skip confirmation
```

## Web UI

```bash
ata serve                       # start at :4400
ata serve --port 8080           # custom port
ata serve --addr 0.0.0.0        # bind to all interfaces
```

htmx-powered dashboard with drag-to-reorder, inline editing, tag autocomplete, dependency editor, and live SSE updates. Also exposes `POST /api/v1/exec` for remote CLI access.

## Remote servers

Configure named remotes so the local `ata` CLI proxies commands to a remote `ata serve`:

```bash
ata remote add NAME URL                 # add or update a remote (first becomes default)
ata remote add NAME URL --default       # add and set as default
ata remote remove NAME                  # remove a remote
ata remote list                         # show configured remotes
```

`snapshot`, `restore`, and `serve` always run locally; other commands transparently proxy through the default remote when configured.

## JSON output

Most commands support `--json` for structured output:
```bash
ata create "task" --json        # returns JSON with task ID
ata list --json                 # JSON array of tasks
ata show ID --json              # JSON task object (or array for multiple IDs)
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

Stdin is polled before being read, so passing positional args (`ata close ID reason`) works fine even when the surrounding shell leaves stdin as an open pipe (e.g. some agent shells) — the command won't hang.

## Common workflows

**Triage new work:**
```bash
ata create "Investigate flaky test in CI" --tag ci,testing
ata create "Refactor auth middleware" --tag backend --status backlog
```

**Plan an epic:**
```bash
ata create "User onboarding redesign" --body-file onboarding.md
ata promote ID                                   # flip to epic; body is preserved
ata create "Design new welcome flow" --epic ID
ata create "Implement email verification" --epic ID
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
- `ata ready` is the best way to find unblocked work — it respects dependencies.
- Use `--json` when you need to parse output programmatically.
- The single-flag form is one dash (`-tag foo`) or two (`--tag foo`); both work.
