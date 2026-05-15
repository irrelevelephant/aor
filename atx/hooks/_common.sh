#!/usr/bin/env bash
# Shared bottom-half for atx's Claude Code hooks. Reads the hook's
# JSON payload from stdin, derives machine + tmux window context,
# and POSTs to atx's hook ingest endpoint.
#
# The wrapper script sets ATX_EVENT before exec'ing this one:
#   ATX_EVENT=Notification exec ~/.claude/atx-hooks/_common.sh
#
# Overrides via env (rarely needed):
#   ATX_SERVER  — atx base URL (default: https://pie.tail1454ae.ts.net)
#   ATX_TIMEOUT — curl timeout in seconds (default: 5)

set -uo pipefail

ATX_SERVER="${ATX_SERVER:-https://pie.tail1454ae.ts.net}"
ATX_TIMEOUT="${ATX_TIMEOUT:-5}"
ATX_EVENT="${ATX_EVENT:-unknown}"

# Pull the canonical machine name from Tailscale: the first label of the
# MagicDNS name (e.g. "mac-mini" from "mac-mini.tail1454ae.ts.net.").
# This matches the entries in atx.toml on the server. The system
# `hostname -s` on macOS returns the device name ("Thomass-Mac-mini")
# which doesn't.
machine=""
if command -v tailscale >/dev/null 2>&1; then
    machine="$(tailscale status --self --json 2>/dev/null | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
    dn = (d.get("Self") or {}).get("DNSName") or ""
    print(dn.split(".", 1)[0])
except Exception:
    pass
' 2>/dev/null)"
fi
if [[ -z "$machine" ]]; then
    machine="$(hostname -s 2>/dev/null || uname -n)"
fi
machine="${machine//[$'\t\r\n ']/}"
session=""; window_index=""; window_name=""
if [[ -n "${TMUX_PANE:-}" ]] && command -v tmux >/dev/null 2>&1; then
    read -r session window_index window_name < <(
        tmux display-message -t "$TMUX_PANE" -p '#S #I #W' 2>/dev/null
    ) || true
fi

payload="$(cat || true)"

# Build the JSON body via python3 so window names / payloads with quotes,
# backslashes, or non-ASCII don't corrupt the request.
body="$(python3 -c '
import json, sys
machine, session, window_index, window_name, event, payload = sys.argv[1:7]
out = {
    "machine": machine,
    "session": session,
    "window_index": window_index,
    "window_name": window_name,
    "event": event,
}
if payload:
    out["payload"] = payload
print(json.dumps(out))
' "$machine" "$session" "$window_index" "$window_name" "$ATX_EVENT" "$payload")"

# Fire-and-forget. Never block the hook on network failure.
curl -sS -m "$ATX_TIMEOUT" \
    -H 'Content-Type: application/json' \
    -X POST \
    --data-raw "$body" \
    "$ATX_SERVER/atx/api/hooks/event" >/dev/null 2>&1 || true

# Claude Code expects hooks to exit 0; never block the prompt.
exit 0
