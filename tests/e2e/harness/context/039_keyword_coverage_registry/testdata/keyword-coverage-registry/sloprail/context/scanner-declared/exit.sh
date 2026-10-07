#!/usr/bin/env bash
# exit: may the context close? Only once the sibling verify-scanner-coverage
# gate passed this Stop — a thin exit mirroring the gate's verdict. (A context's
# exit does not itself refuse a Stop; the gate does.)
#
# Staying open while coverage is refused is what keeps the refusal standing. The
# gate only runs while this context is active, and the context is only re-entered
# by a Post event for a scanner file that still differs from the session's
# baseline. Measured on a real run: refused for an uncovered scanner, a sub-agent
# ran `rm -rf scanners/<name>` — the file was gone, no Post event re-entered this
# context, an exit that always closed had closed it at the refused Stop, and the
# gate never ran again. Kept open, the gate runs at every Stop and reads the
# registry, which still holds the scanner whether or not its file survives.
set -uo pipefail

input="$(cat)"
status="$(printf '%s' "$input" | jq -r '.gates["verify-scanner-coverage"].status // "fail"' 2>/dev/null)"
[ "$status" = "pass" ] && exit 0
exit 1
