#!/usr/bin/env bash
# exit: pure lifecycle tracking — a context's exit does not itself refuse a
# Stop. The sibling verify-scanner-coverage gate reads the registry this context
# wrote into and refuses on missing coverage.
set -uo pipefail
cat
