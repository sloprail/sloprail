#!/usr/bin/env bash
# exit: pure lifecycle tracking, same split as everywhere else in this
# corpus (2026-08-19, slice 7 — a context's exit does not itself refuse a
# Stop). The sibling verify-scanner-coverage gate is what reads the
# registry this context wrote into and refuses on missing coverage.
set -uo pipefail
cat
