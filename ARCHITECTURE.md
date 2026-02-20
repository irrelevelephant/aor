# AOR Architecture — How the Agent Orchestrator Calls Claude Code

## How it calls Claude Code

The core is in `session.go:20-269` — the `runSession` function. It spawns Claude Code as a **child process** via `exec.Command`:

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
- **`-p <prompt>`** — runs Claude in non-interactive (headless) mode with the prompt as the sole input
- **`--output-format stream-json`** — Claude emits one JSON object per line on stdout instead of plain text
- **`--dangerously-skip-permissions`** — (default on, `--no-yolo` to disable) lets the agent run tools without human approval

## Input: How prompts are constructed

There are **three prompt builders**, each for a different mode:

1. **Task execution** (`runner.go:16-75`, `buildPrompt`) — Tells Claude it has a pre-claimed beads task to work on, instructs it to implement, commit, and close the task. Includes batch size (how many additional tasks to pick up) and scope labels.

2. **Code review** (`review_prompt.go:11-105`, `buildReviewPrompt`) — Inlines a git diff and asks Claude to find/fix issues across 6 priority areas. Used by `aor rev`.

3. **Post-task review** (`review_prompt.go:110-222`, `buildPostTaskReviewPrompt`) — After each completed task, a separate session reviews the diff across 8 dimensions (correctness, security, performance, etc.).

All prompts end with a **sentinel instruction** — a required structured JSON line the agent must output as its final action so the orchestrator can parse results.

## Output: How results come back

### Stream processing (`session.go:111-157`)

Claude's stdout is piped and read line-by-line. Each line is a `ClaudeStreamMsg` JSON object with a `type` field:

| Type | What happens |
|------|-------------|
| `system` (subtype `init`) | Logs session init, captures `session_id` |
| `assistant` | Text is printed bold; tool calls are rendered with a gutter (`│`) — Edit calls show syntax-highlighted diffs, Bash shows the command, etc. |
| `user` | Tool results — suppressed to avoid noise |
| `result` | Final message — captures token usage, cost, turn count, duration |

All raw JSON is also written to a per-session log file.

### Sentinel parsing (`session.go:432-489`)

After the session ends, `parseSentinelJSON` scans the raw output for a magic prefix like `BEADS_RUNNER_STATUS:` or `REVIEW_STATUS:` followed by JSON. It tries two strategies:
1. Per-line scan (fast path)
2. Concatenated text scan (handles streaming splits across messages)

This gives the orchestrator structured data like:
```json
BEADS_RUNNER_STATUS:{"completed": ["T-42"], "discovered": ["T-99"], ...}
```

## The orchestration loop (`runner.go:104-413`)

```
┌─────────────────────────────────────────┐
│  bd ready --json  →  get ready tasks    │
│         ↓                               │
│  topTask() → pick highest priority      │
│         ↓                               │
│  bd update <id> --claim  (pre-claim)    │
│         ↓                               │
│  buildPrompt() → construct instructions │
│         ↓                               │
│  runSession() → spawn `claude -p ...`   │
│    ├── stream stdout (display + log)    │
│    ├── monitor stdin (i/s/q controls)   │
│    └── handle Ctrl+C signals            │
│         ↓                               │
│  parseSentinelJSON → extract status     │
│         ↓                               │
│  If completed: update stats             │
│  If not: unclaim task, track failure    │
│         ↓                               │
│  Post-task review session (optional)    │
│         ↓                               │
│  Loop back to top (3s pause)            │
└─────────────────────────────────────────┘
```

## Interactive controls during a session

A goroutine reads stdin via a shared channel (`startStdinReader`). While Claude is running:

- **`i` + Enter** — Kills the headless session, then runs `claude --resume <session_id>` with full stdin/stdout/stderr attached, giving you an interactive terminal session. When you exit, the runner loop continues.
- **`s` + Enter** — Kills the session, unclaims the task, moves to next.
- **`q` + Enter** — Gracefully stops, finishes current session, exits.
- **Ctrl+C** — Sends SIGINT to Claude. Double-press within 2s force-kills it.

## The `aor rev` subcommand (`review.go`)

A separate iterative review loop:
1. Compute `git diff <base>...HEAD` + working tree changes
2. Spawn a Claude session with the review prompt
3. Parse `REVIEW_STATUS:` sentinel
4. Check convergence (no issues, all minor, repeating issues, HEAD cycling)
5. Repeat up to `--max-rounds` (default 3)
6. If uncommitted fixes remain, run a final "commit sweep" session

## Summary

The entire system is a **process-level orchestrator** — it never uses the Claude API directly. It shells out to the `claude` CLI in headless mode (`-p` + `--output-format stream-json`), reads structured JSON from stdout line by line, renders a curated terminal UI (syntax-highlighted diffs, tool gutters), and coordinates task state through the `bd` CLI. The sentinel pattern (magic prefix + JSON) is the contract between the outer Go orchestrator and the inner Claude agent for passing structured results back.
