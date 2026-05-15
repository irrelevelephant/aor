#!/usr/bin/env bash
# Claude Code `Stop` hook → atx push.
# Fires when Claude finishes a turn / response.
ATX_EVENT=Stop exec "$(dirname "$0")/_common.sh"
