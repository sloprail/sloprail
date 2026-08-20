#!/usr/bin/env bash
# enter: only called when the on-trigger's own `match` already confirmed
# #research is among this turn's tags — no re-checking here, that would
# duplicate what the declarative match already settled.
set -uo pipefail

jq -n '{declared: true}'
