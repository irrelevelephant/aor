# afl — Agent Flows

Design spec for a platform-parity auditing tool that tracks UI flows across web (desktop + mobile), iOS, and Android via step-level screenshots.

## Overview

**afl** lives alongside `ata` in the `~/aor` repo. It is a visual companion to the existing `specs/flows.md` documentation — flows.md remains the behavioral source of truth while afl adds screenshot-based parity tracking on top, cross-referencing flow IDs.

**Primary purpose:** Detecting visual and behavioral differences across platforms by capturing step-level screenshots and presenting them in a side-by-side grid.

**Screenshot sources:**
- **Web (desktop + mobile):** Playwright via `agent-browser` or direct Playwright API
- **iOS:** XcodeBuildMCP `screenshot` tool (captures simulator screen as PNG)
- **Android:** DroidMind `android-screenshot` tool (captures device/emulator screen as PNG)
- **Manual:** Direct file upload via CLI or web UI

## Architecture

```
~/aor/
├── go.work              # Go workspace (references ./ata, ./afl)
├── ata/                 # Existing: task tracking CLI + library
│   ├── main.go          # CLI entry point
│   ├── cmd/             # Command handlers
│   ├── db/              # SQLite layer
│   ├── model/           # Data structures
│   ├── web/             # Web routes + templates (ata-specific)
│   ├── client/          # Remote client
│   ├── config/          # Config (~/.ata/)
│   └── api/             # Shared API types
├── afl/                 # New: flow tracking CLI + library
│   ├── main.go          # CLI entry point
│   ├── cmd/             # Command handlers
│   ├── db/              # SQLite layer (~/.afl/afl.db)
│   ├── model/           # Data structures
│   ├── web/             # Web routes + templates (afl-specific)
│   ├── client/          # Remote client
│   ├── config/          # Config (~/.afl/)
│   ├── api/             # Shared API types
│   └── parser/          # flows.md parser
├── aor/                 # New: unified server + orchestrator
│   ├── main.go          # `aor` binary entry point
│   ├── cmd/
│   │   ├── serve.go     # `aor serve` — unified web server
│   │   ├── run.go       # existing orchestrator logic (from current root)
│   │   ├── pull.go
│   │   ├── merge.go
│   │   ├── rev.go
│   │   └── spec.go
│   └── server/          # Shared server infrastructure
│       ├── server.go    # HTTP mux, SSE hub, TLS
│       └── sse.go       # Shared SSE event broadcasting
└── Makefile             # Builds ata, afl, aor binaries
```

### Server Unification

`aor serve` replaces `ata serve` as the unified web server:
- Mounts ata web routes at `/` (preserving existing URLs)
- Mounts afl web routes at `/afl/`
- Shared SSE endpoint at `/events` broadcasts both ata and afl events
- Single port (default 4400), single process
- Both ata and afl CLIs proxy to this server when a remote is configured

### Remote Proxy

Same pattern as ata:
- `afl` CLI checks `~/.afl/config.json` for remote configuration
- Commands proxy to `POST /api/v1/afl/exec` with `{command, args}`
- Screenshot uploads use `POST /api/v1/afl/upload` with multipart form data
- Screenshots are stored only on the server — single source of truth
- Upload streams directly to the remote server (no local buffering)

## Data Model

### Database: `~/.afl/afl.db` (SQLite, WAL mode)

```sql
-- Schema version tracking
CREATE TABLE schema_version (version INTEGER NOT NULL);

-- Workspaces (same concept as ata)
CREATE TABLE workspaces (
    path    TEXT PRIMARY KEY,
    name    TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Domains (map to specs/ subdirectories: water, diary, check-ins, etc.)
CREATE TABLE domains (
    id         TEXT PRIMARY KEY,        -- short base36 ID
    workspace  TEXT NOT NULL REFERENCES workspaces(path),
    slug       TEXT NOT NULL,           -- e.g., "water", "diary", "check-ins"
    name       TEXT NOT NULL,           -- e.g., "Water Tracking", "Diary"
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    UNIQUE(workspace, slug)
);

-- Flows (map to flow IDs in flows.md: WATER-LOG-ENTRY, DIARY-ADD-MEAL, etc.)
CREATE TABLE flows (
    id         TEXT PRIMARY KEY,        -- short base36 ID
    domain_id  TEXT NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    flow_id    TEXT NOT NULL,           -- spec flow ID, e.g., "WATER-LOG-ENTRY"
    name       TEXT NOT NULL,           -- e.g., "Add Water Entry"
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    sort_order INTEGER NOT NULL DEFAULT 0,
    UNIQUE(domain_id, flow_id)
);

-- Paths within a flow (happy path, alternate paths, error paths)
CREATE TABLE paths (
    id         TEXT PRIMARY KEY,        -- short base36 ID
    flow_id    TEXT NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
    path_type  TEXT NOT NULL CHECK(path_type IN ('happy', 'alternate', 'error')),
    name       TEXT NOT NULL,           -- e.g., "Happy path", "Alternate: existing entry"
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Steps within a path
CREATE TABLE steps (
    id          TEXT PRIMARY KEY,       -- short base36 ID
    path_id     TEXT NOT NULL REFERENCES paths(id) ON DELETE CASCADE,
    sort_order  INTEGER NOT NULL,
    name        TEXT NOT NULL,          -- e.g., "Tap + button on water widget"
    description TEXT,                   -- optional longer description
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Screenshots (one per step per platform — latest only, replaced on re-upload)
CREATE TABLE screenshots (
    id             TEXT PRIMARY KEY,    -- UUID
    step_id        TEXT NOT NULL REFERENCES steps(id) ON DELETE CASCADE,
    platform       TEXT NOT NULL CHECK(platform IN ('web-desktop', 'web-mobile', 'ios', 'android')),
    filename       TEXT NOT NULL,       -- original filename
    stored_name    TEXT NOT NULL,       -- on-disk name (UUID-based)
    mime_type      TEXT NOT NULL,       -- image/png, image/jpeg
    size_bytes     INTEGER NOT NULL,
    capture_source TEXT,                -- playwright, xcodebuildmcp, droidmind, manual
    captured_at    TEXT NOT NULL,       -- when the screenshot was taken
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    UNIQUE(step_id, platform)           -- one screenshot per step per platform
);

-- Indices
CREATE INDEX idx_domains_workspace ON domains(workspace);
CREATE INDEX idx_flows_domain ON flows(domain_id);
CREATE INDEX idx_paths_flow ON paths(flow_id);
CREATE INDEX idx_steps_path ON steps(path_id);
CREATE INDEX idx_screenshots_step ON screenshots(step_id);
CREATE INDEX idx_screenshots_platform ON screenshots(platform);
```

### Entity Hierarchy

```
Workspace
 └── Domain (water, diary, check-ins, ...)
      └── Flow (WATER-LOG-ENTRY, DIARY-ADD-MEAL, ...)
           └── Path (happy, alternate:existing-entry, error:validation, ...)
                └── Step (1: Open modal, 2: Enter amount, 3: Save, ...)
                     └── Screenshot × Platform (web-desktop, web-mobile, ios, android)
```

### Platforms (fixed enum)

| Value | Description |
|-------|-------------|
| `web-desktop` | Web app in desktop viewport (Playwright, ~1280×800) |
| `web-mobile` | Web app in mobile viewport (Playwright, ~390×844) |
| `ios` | iOS app on simulator (XcodeBuildMCP screenshot) |
| `android` | Android app on emulator/device (DroidMind screenshot) |

### Capture Sources (tracked per screenshot)

| Value | Tool |
|-------|------|
| `playwright` | Playwright / agent-browser |
| `xcodebuildmcp` | XcodeBuildMCP screenshot tool |
| `droidmind` | DroidMind android-screenshot tool |
| `manual` | Direct file upload |

## CLI Design

All commands follow ata's noun-verb pattern. Global flags: `--workspace`, `--json`, `--remote`.

### Workspace Management

```bash
afl init <path> [--name <name>]        # Register workspace
afl config                              # Manage config (defaults, remotes)
```

### Domain Commands

```bash
afl domain create <slug> [--name <display-name>] [--workspace <ws>]
afl domain list [--workspace <ws>]
afl domain show <slug> [--workspace <ws>]
afl domain delete <slug> [--workspace <ws>]
```

### Flow Commands

```bash
afl flow create <domain-slug> <FLOW-ID> <name> [--workspace <ws>]
afl flow list [--domain <slug>] [--workspace <ws>]
afl flow show <FLOW-ID> [--workspace <ws>]
afl flow delete <FLOW-ID> [--workspace <ws>]
afl flow import <flows.md> [--workspace <ws>]   # Parse + create domain/flows/paths/steps
```

### Path Commands

```bash
afl path create <FLOW-ID> --type <happy|alternate|error> --name <name>
afl path list <FLOW-ID>
afl path delete <path-id>
```

### Step Commands

```bash
afl step create <path-id> --name <name> [--description <desc>] [--order <n>]
afl step list <path-id>
afl step edit <step-id> [--name <name>] [--description <desc>]
afl step delete <step-id>
```

### Capture Commands (screenshots)

```bash
# Upload a single screenshot for a specific step + platform
afl capture upload <FLOW-ID> <step-order> <platform> <image-path> \
    [--path <path-name>] [--source <tool>] [--workspace <ws>]

# Batch upload: directory of numbered images (1.png, 2.png, ...)
afl capture batch <FLOW-ID> <platform> <dir> \
    [--path <path-name>] [--source <tool>] [--workspace <ws>]

# View capture status for a flow
afl capture status <FLOW-ID> [--workspace <ws>]

# Download a screenshot (by step + platform)
afl capture get <FLOW-ID> <step-order> <platform> \
    [--path <path-name>] [--output <file>] [--workspace <ws>]
```

### Remote Management

```bash
afl remote add <name> <url> [--default]
afl remote list
afl remote remove <name>
```

### Config File: `~/.afl/config.json`

```json
{
    "default_workspace": "trackit.fit",
    "workspaces": {
        "/Users/tjs/trackit.fit": "trackit.fit"
    },
    "remotes": {
        "pie": {
            "url": "https://pie.tail1454ae.ts.net/",
            "workspace": "trackit.fit"
        }
    },
    "default_remote": "pie"
}
```

## flows.md Parser

### Strict Format Expectations

The parser expects the exact format used in trackit.fit's `specs/*/flows.md`:

```markdown
# Domain — UX Flows

## FLOW-ID: Flow Name

**Precondition**: ...
**Trigger**: ...

### Happy path

1. Step description
   → `[observable UI state]`
2. Next step
   → `[UI state]`

### Alternate: Description

3a. Alternate step
    → `[UI state]`

### Error: Description

3e. Error step
    → `[UI state]`

**E2E**: `e2e/tests/domain/file.spec.ts`
```

### Parser Behavior

1. **Domain extraction**: Parses the `# Domain — UX Flows` header to extract domain slug (lowercased, hyphenated)
2. **Flow extraction**: Each `## FLOW-ID: Flow Name` becomes a flow record
3. **Path extraction**: `### Happy path`, `### Alternate: ...`, `### Error: ...` become paths with appropriate `path_type`
4. **Step extraction**: Numbered lines (`1.`, `2.`, `3a.`, `3e.`) become steps. The number is the sort order, the text is the step name
5. **Observable state**: Lines matching `→ \`[...]\`` become the step description
6. **Platform notes**: Ignored (stays in flows.md only)
7. **E2E references**: Ignored (not part of afl's scope)
8. **Auto-create**: Domain is auto-created if it doesn't exist
9. **Idempotent**: Re-importing updates existing flows/steps (matched by flow_id), doesn't duplicate

### Import Example

```bash
# Import a single domain's flows
afl flow import specs/water/flows.md --workspace trackit.fit

# Import all domains
for f in specs/*/flows.md; do
    afl flow import "$f" --workspace trackit.fit
done
```

## Web UI

### Route Structure

All afl routes live under `/afl/` prefix within the unified `aor serve` server.

| Route | Page | Description |
|-------|------|-------------|
| `/afl/` | Dashboard | Workspace selector (if multiple) |
| `/afl/w/<workspace>` | Coverage Dashboard | Domains with coverage stats |
| `/afl/d/<domain-id>` | Domain Detail | Flows within domain, per-flow coverage |
| `/afl/f/<flow-id>` | Flow Grid | Side-by-side step × platform grid |
| `/afl/f/<flow-id>/<path-id>` | Path Grid | Grid for a specific path |
| `/afl/screenshots/<id>` | Screenshot | Full-size screenshot (direct image) |

### API Routes

| Route | Method | Description |
|-------|--------|-------------|
| `/api/v1/afl/exec` | POST | CLI command proxy (same pattern as ata) |
| `/api/v1/afl/upload` | POST | Multipart screenshot upload |
| `/api/v1/afl/screenshots/<id>` | GET | Serve screenshot image |

### Coverage Dashboard (`/afl/w/<workspace>`)

Shows all domains with coverage statistics:

```
┌─────────────────────────────────────────────────────────┐
│  trackit.fit — Flow Coverage                            │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  water          ████████░░  5/7 flows fully covered     │
│  diary          ███░░░░░░░  3/12 flows fully covered    │
│  check-ins      ██████████  4/4 flows fully covered     │
│  meal-planning  ░░░░░░░░░░  0/8 flows covered           │
│  biometrics     █████░░░░░  2/5 flows fully covered     │
│  settings       ████████░░  6/8 flows fully covered     │
│  ...                                                    │
│                                                         │
│  Overall: 20/44 flows fully covered (45%)               │
└─────────────────────────────────────────────────────────┘
```

"Fully covered" = all steps in the happy path have screenshots for all 4 platforms.

Clicking a domain navigates to the domain detail page.

### Domain Detail (`/afl/d/<domain-id>`)

Lists all flows within a domain with per-flow coverage breakdown:

```
┌─────────────────────────────────────────────────────────────────────┐
│  water — Flows                                                      │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  WATER-LOG-ENTRY: Add Water Entry                                   │
│  Happy path: 5 steps    web-d ✓  web-m ✓  ios ✓  android ✓         │
│  Alt: existing entry: 3 steps   web-d ✓  web-m ✗  ios ✗  android ✗ │
│                                                                     │
│  WATER-DAILY-GOAL: Set Daily Goal                                   │
│  Happy path: 4 steps    web-d ✓  web-m ✓  ios ✗  android ✗         │
│                                                                     │
│  WATER-HISTORY: View History                                        │
│  Happy path: 3 steps    web-d ✗  web-m ✗  ios ✗  android ✗         │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

Coverage checkmark (✓) means all steps in that path have a screenshot for that platform. Partial coverage shows a fraction (e.g., "3/5").

### Flow Grid (`/afl/f/<flow-id>`)

The primary parity auditing view. Side-by-side grid with:
- **Rows** = steps (ordered by sort_order)
- **Columns** = platforms (web-desktop, web-mobile, ios, android)
- **Cells** = thumbnail screenshots (aspect-preserving)
- **Tabs** = path selector (Happy | Alternate: ... | Error: ...)

```
┌──────────────────────────────────────────────────────────────────────────┐
│  WATER-LOG-ENTRY: Add Water Entry                                        │
│  [Happy path]  [Alt: existing entry]  [Error: validation]                │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  Step          web-desktop    web-mobile     ios            android       │
│  ─────────────────────────────────────────────────────────────────────── │
│                                                                          │
│  1. Open       ┌─────────┐   ┌───────┐     ┌───────┐     ┌───────┐     │
│     water      │         │   │       │     │       │     │       │     │
│     modal      │  1280×  │   │ 390×  │     │ 390×  │     │ 390×  │     │
│                │   800   │   │  844  │     │  844  │     │  844  │     │
│                └─────────┘   └───────┘     └───────┘     └───────┘     │
│                                                                          │
│  2. Enter      ┌─────────┐   ┌───────┐     ┌───────┐     ┌───────┐     │
│     amount     │         │   │       │     │       │     │       │     │
│                │         │   │       │     │       │     │       │     │
│                └─────────┘   └───────┘     └───────┘     └───────┘     │
│                                                                          │
│  3. Save       ┌─────────┐   ┌───────┐     ┌───────┐       ╳           │
│     entry      │         │   │       │     │       │     (missing)      │
│                └─────────┘   └───────┘     └───────┘                    │
│                                                                          │
└──────────────────────────────────────────────────────────────────────────┘
```

**Aspect-preserving layout:** Desktop column is wider than mobile columns. Mobile columns (web-mobile, ios, android) share similar aspect ratios and are given equal width.

**Lightbox:** Clicking any thumbnail opens a full-size lightbox overlay with:
- Full-resolution screenshot
- Platform label and capture metadata (source tool, capture timestamp)
- Prev/next navigation within the step (across platforms)
- Up/down navigation across steps (same platform)
- Keyboard shortcuts: arrow keys for navigation, Escape to close

**Missing screenshots:** Shown as a placeholder cell with an ✗ icon. Immediately visible where coverage gaps exist.

### SSE Events

afl broadcasts events through the shared `/events` SSE endpoint:

| Event | Data | Trigger |
|-------|------|---------|
| `afl_screenshot_uploaded` | `{flow_id, step_id, platform}` | New screenshot uploaded |
| `afl_flow_updated` | `{flow_id}` | Flow/path/step created/modified/deleted |
| `afl_domain_updated` | `{domain_id}` | Domain created/modified/deleted |

## Agent Capture Workflow

### Web (Playwright / agent-browser)

```bash
# Agent navigates through flow, capturing each step
agent-browser open "http://localhost:3000/water"
agent-browser screenshot --output /tmp/water-log-1-desktop.png
afl capture upload WATER-LOG-ENTRY 1 web-desktop /tmp/water-log-1-desktop.png \
    --source playwright --workspace trackit.fit

# For mobile viewport
agent-browser open "http://localhost:3000/water" --viewport 390x844
agent-browser screenshot --output /tmp/water-log-1-mobile.png
afl capture upload WATER-LOG-ENTRY 1 web-mobile /tmp/water-log-1-mobile.png \
    --source playwright --workspace trackit.fit
```

### iOS (XcodeBuildMCP)

Claude Code uses XcodeBuildMCP's `screenshot` tool to capture the simulator screen:

```bash
# After navigating to the correct screen in the iOS simulator
# XcodeBuildMCP screenshot captures to a file path
afl capture upload WATER-LOG-ENTRY 1 ios /tmp/ios-water-log-1.png \
    --source xcodebuildmcp --workspace trackit.fit
```

### Android (DroidMind)

Claude Code uses DroidMind's `android-screenshot` tool to capture the emulator/device:

```bash
# After navigating to the correct screen on the Android device
# DroidMind screenshot captures to a file path
afl capture upload WATER-LOG-ENTRY 1 android /tmp/android-water-log-1.png \
    --source droidmind --workspace trackit.fit
```

### Batch Capture

For any platform, if the agent captures all steps to a numbered directory:

```bash
# Agent captures screenshots as 1.png, 2.png, 3.png, ...
afl capture batch WATER-LOG-ENTRY web-desktop /tmp/water-log-captures/ \
    --source playwright --workspace trackit.fit
```

The batch command maps filenames to step sort_order: `1.png` → step 1, `2.png` → step 2, etc.

## File Storage

### Server-side layout

```
~/.afl/
├── afl.db                          # SQLite database
├── config.json                     # CLI config (remotes, defaults)
└── screenshots/                    # Screenshot storage
    └── <flow-id>/
        └── <step-id>/
            └── <platform>/
                └── <uuid>.png      # Stored screenshot
```

### Storage rules

- Screenshots are stored **only on the server** (the machine running `aor serve`)
- CLI uploads stream directly to the remote server via multipart POST
- When running locally (no remote configured), screenshots store in `~/.afl/screenshots/`
- Old screenshots for the same step+platform are deleted when a new one is uploaded (latest only)
- No local caching or buffering

## Implementation Plan

### Phase 1: Foundation
1. Set up `afl/` Go module in `~/aor` alongside `ata/`
2. SQLite schema, migrations, database layer
3. Core model types (Workspace, Domain, Flow, Path, Step, Screenshot)
4. CLI skeleton with workspace, domain, flow, path, step CRUD commands
5. `--json` output on all commands

### Phase 2: Capture & Storage
6. Screenshot upload/storage (local mode)
7. `afl capture upload` (single) and `afl capture batch` commands
8. `afl capture status` command
9. `afl capture get` (download) command

### Phase 3: Parser
10. flows.md parser (strict format)
11. `afl flow import` command
12. Domain auto-creation on import
13. Idempotent re-import (update existing, don't duplicate)

### Phase 4: Server Unification
14. Extract shared server infrastructure from `ata/web/` into `aor/server/`
15. Create `aor` Go module with `aor serve` command
16. Mount ata routes at `/`, afl routes at `/afl/`
17. Shared SSE event broadcasting
18. Remote proxy support for afl (`/api/v1/afl/exec`, `/api/v1/afl/upload`)
19. Migrate existing orchestrator commands into `aor` binary

### Phase 5: Web UI
20. Coverage dashboard page (`/afl/w/<workspace>`)
21. Domain detail page (`/afl/d/<domain-id>`)
22. Flow grid page (`/afl/f/<flow-id>`) — side-by-side step × platform grid
23. Lightbox overlay for full-size screenshot viewing
24. Path tab navigation
25. Screenshot serving endpoint (`/api/v1/afl/screenshots/<id>`)
26. SSE integration for live updates

### Phase 6: Remote & Polish
27. Remote proxy in afl CLI (mirror ata's `client/` package)
28. Multipart upload streaming to remote
29. Error handling, validation, edge cases
30. Update Makefile to build all three binaries
