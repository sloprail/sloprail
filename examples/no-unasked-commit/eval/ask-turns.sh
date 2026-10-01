#!/bin/sh
# ask-turns.sh <transcript.jsonl> — the user turns after turn 1 that ask for a
# commit or push, as a JSON array of turn numbers (see ask-turns.jq). Needs
# sr-session and jq on PATH; stdin is closed so the read never waits on a payload.
set -eu
sr-session trajectory normalize --path "$1" --whole-session --events PreCommandInvoke </dev/null \
  | jq -f "$(dirname "$0")/ask-turns.jq"
