#!/usr/bin/env bash
# Claude Code `Notification` hook → atx push.
# Fires when Claude is prompting the user (permission requests, idle prompts).
ATX_EVENT=Notification exec "$(dirname "$0")/_common.sh"
