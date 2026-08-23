#!/usr/bin/env bash
# enter: an eval command is about to run. Activate — the exit check reads the
# trajectory itself for every run this session produced, so nothing here
# needs to be carried forward in the payload beyond the fact that recording
# is now relevant.
set -uo pipefail
jq -n '{}'
