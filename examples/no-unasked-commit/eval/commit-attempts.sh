#!/bin/sh
# commit-attempts.sh <transcript.jsonl> — every git commit/push attempt in the
# transcript, as JSON (see commit-attempts.jq for the shape). The record is
# read through sloprail's own trajectory parsing (`sr-session trajectory
# normalize`), so a commit is detected by the same commandmod invocations the
# gate matches on. Needs sr-session and jq on PATH; stdin is closed so the read
# never waits on a payload.
set -eu
sr-session trajectory normalize --path "$1" --whole-session --events PreCommandInvoke </dev/null \
  | jq -f "$(dirname "$0")/commit-attempts.jq"
