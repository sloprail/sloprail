#!/usr/bin/env bash
# enter: only called when the on-trigger's own `match` already confirmed
# #research is among this turn's tags — no re-checking here.
set -uo pipefail

jq -n '{declared: true}'
