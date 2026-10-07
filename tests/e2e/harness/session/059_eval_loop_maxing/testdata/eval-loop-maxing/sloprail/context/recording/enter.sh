#!/usr/bin/env bash
# enter: an eval command is about to run. Just activate — the exit check reads
# the trajectory for every run this session produced, so nothing needs to be
# carried in the payload.
set -uo pipefail
cat >/dev/null
jq -n '{}'
