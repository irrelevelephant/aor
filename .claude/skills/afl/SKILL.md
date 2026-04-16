---
name: afl
description: >
  Use afl (Agent FLows) to track UI flows and manage cross-platform screenshot parity.
  Trigger when the user asks to create, list, or delete domains/flows/paths/steps,
  import flows from a flows.md file, upload or batch-upload screenshots, check
  screenshot coverage, or view the flow grid. Also trigger on phrases like
  "import flows", "capture screenshots", "check parity", "screenshot coverage",
  or "flow grid".
argument-hint: "[subcommand] [args...]"
allowed-tools: Bash
---

# afl — Agent FLows

`afl` is a SQLite-backed flow tracker at `~/.afl/afl.db`. It stores UI flows (mirroring the structure of `specs/*/flows.md`) and step-level screenshots for cross-platform parity auditing. All data is scoped to a **workspace** (auto-detected from git root). The `afl` binary should be on $PATH.

## Entity hierarchy

```
Workspace → Domain → Flow → Path (happy | alternate | error) → Step → Screenshot × Platform
```

**Platforms**: `web-desktop`, `web-mobile`, `ios`, `android`
**Sources**: `playwright`, `xcodebuildmcp`, `droidmind`, `manual`

One screenshot per (step, platform) — re-uploading replaces the previous one.

## Domains

```bash
afl domain create water --name "Water Tracking"
afl domain list
afl domain show water
afl domain delete water
```

## Flows

```bash
afl flow create <domain-slug> <FLOW-ID> <name>   # e.g., afl flow create water WATER-LOG-ENTRY "Add Water Entry"
afl flow list [--domain <slug>]
afl flow show <FLOW-ID>
afl flow delete <FLOW-ID>
afl flow import <path/to/flows.md>               # parse + upsert domain/flows/paths/steps (idempotent)
```

`afl flow import` expects the standard `specs/*/flows.md` format:
- `# Domain — UX Flows` → domain slug
- `## FLOW-ID: Name` → flow
- `### Happy path`, `### Alternate: ...`, `### Error: ...` → paths
- Numbered lines (`1.`, `2.`, `3a.`, `3e.`) → steps

Platform notes and E2E references in the file are ignored.

## Paths

```bash
afl path create <FLOW-ID> --type <happy|alternate|error> --name "<name>" [--order <n>]
afl path list <FLOW-ID>
afl path delete <path-id>
```

Most flows only need the auto-created happy path from `afl flow import`.

## Steps

```bash
afl step create <path-id> --name "<name>" [--description "<desc>"] [--order <n>]
afl step list <path-id>
afl step edit <step-id> [--name "<name>"] [--description "<desc>"]
afl step delete <step-id>
```

## Capture (screenshots)

```bash
# Single screenshot (maps by step order within the path)
afl capture upload <FLOW-ID> <step-order> <platform> <image-path> \
    [--path <path-name>] [--source <tool>]

# Batch: directory of 1.png, 2.png, 3.png, ... (filename = step order)
afl capture batch <FLOW-ID> <platform> <dir> \
    [--path <path-name>] [--source <tool>]

# Coverage summary for a flow
afl capture status <FLOW-ID>

# Download a screenshot
afl capture get <FLOW-ID> <step-order> <platform> [--path <name>] [--output <file>]
```

If `--path` is omitted, the happy path is used. `--source` defaults to `manual`.

## Web UI

The `afl` UI is part of the unified `aor serve` server — there is no `afl serve`.

```bash
aor serve                # :4400 by default
# ata UI: http://localhost:4400/
# afl UI: http://localhost:4400/afl/
```

The afl UI provides:
- **Coverage dashboard** (`/afl/w/<workspace>`) — domain-level stats
- **Domain detail** (`/afl/d/<domain-id>`) — per-flow coverage
- **Flow grid** (`/afl/f/<flow-id>`) — step × platform grid with lightbox

## Remote servers

Proxy all afl commands to a remote `aor serve`:

```bash
afl remote add <name> <url> [--default]
afl remote list
afl remote remove <name>
```

Once a default remote is configured, screenshot uploads stream directly to the server — screenshots are **only** stored on the server.

## JSON output

Most commands accept `--json` for structured output suitable for piping:

```bash
afl flow list --json
afl capture status WATER-LOG-ENTRY --json
```

## Common workflows

**Bootstrap a domain from a flows.md file:**
```bash
afl flow import specs/water/flows.md
```

**Import every domain's flows:**
```bash
for f in specs/*/flows.md; do afl flow import "$f"; done
```

**Capture a flow on one platform:**
```bash
# Agent produces 1.png, 2.png, ... in /tmp/water-log/
afl capture batch WATER-LOG-ENTRY web-desktop /tmp/water-log --source playwright
afl capture status WATER-LOG-ENTRY
```

**Single capture after agent navigates to a step:**
```bash
afl capture upload WATER-LOG-ENTRY 3 ios /tmp/ios-3.png --source xcodebuildmcp
```

## Tips

- IDs are short base36 strings; flow IDs are the human-readable spec IDs (`WATER-LOG-ENTRY`).
- `afl flow import` is idempotent — re-running updates existing records in place.
- Screenshots are deduplicated per (step, platform) — the latest upload wins.
- For cross-platform parity, capture the same path on all four platforms, then open the flow grid to diff visually.
