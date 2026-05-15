# atx Claude Code hooks

Two tiny bash scripts that turn Claude Code's `Notification` and `Stop`
events into atx push notifications. Install on every Tailnet host you
run Claude Code on.

## What gets installed

Per host, three files under `~/.claude/atx-hooks/`:

| File | Purpose |
| --- | --- |
| `_common.sh` | Reads the CC hook JSON payload from stdin, derives `(machine, session, window_index, window_name)` from `hostname` + `tmux display-message`, and POSTs to `${ATX_SERVER:-https://pie.tail1454ae.ts.net}/atx/api/hooks/event`. |
| `atx-cc-notify.sh` | Wrapper that sets `ATX_EVENT=Notification` and execs `_common.sh`. Wired to CC's `Notification` event (prompts, permission requests, idle). |
| `atx-cc-stop.sh` | Same for `ATX_EVENT=Stop`. Fires on every turn end. |

Hooks are fire-and-forget: they never block the prompt and always
`exit 0` even on network failure.

## Dependencies on the remote

- `bash`, `curl`, `python3` (for safe JSON construction)
- `tailscale` (for the canonical machine name — falls back to
  `hostname -s` if absent, which on macOS gives the device name like
  `Thomass-Mac-mini` rather than the Tailnet alias)
- `tmux` (only used to enrich the event; absent tmux just leaves the
  fields empty)

## Install

The files live in this repo at `atx/hooks/`. Recommended path is via
`~/dev-vm/sync.sh` (a one-line rsync added in this step), which keeps
them in sync across every Tailnet host alongside the other configs.
Manual install on a single host:

```sh
DEST=~/.claude/atx-hooks
mkdir -p "$DEST"
rsync -av /path/to/atx/hooks/{_common.sh,atx-cc-notify.sh,atx-cc-stop.sh} "$DEST/"
chmod +x "$DEST"/*.sh
```

Then add to `~/.claude/settings.json` on each host (merge with any
existing `hooks` block):

```json
{
  "hooks": {
    "Notification": [
      { "hooks": [{ "type": "command", "command": "~/.claude/atx-hooks/atx-cc-notify.sh" }] }
    ],
    "Stop": [
      { "hooks": [{ "type": "command", "command": "~/.claude/atx-hooks/atx-cc-stop.sh" }] }
    ]
  }
}
```

## Smoke test

In a tmux window on the remote, run any Claude Code command that
triggers a permission prompt (e.g. ask for a `Bash` action that's
not allow-listed). Your phone should receive a push within ~2s with
the title `claude · <hostname> · <window>`. Tapping it deep-links to
`/atx/m/<hostname>/w/<window>` in the atx PWA.

Curl-equivalent (skipping the actual hook invocation):

```sh
curl -X POST -H 'Content-Type: application/json' \
  -d '{"machine":"donut","window_index":"2","window_name":"build","event":"Notification","message":"test"}' \
  https://pie.tail1454ae.ts.net/atx/api/hooks/event
```

## Suppression

Not currently implemented. Every Notification/Stop fires a push to
every subscribed device, regardless of whether someone has the
matching window open in the PWA. The `notifications` table has a
`suppressed` column ready for per-viewer suppression to be added
later without a migration.
