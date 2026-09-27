#!/usr/bin/env bash
# The gh call about to run is a GitHub search (gate.yaml's match decided that on
# the parsed invocations). Permit it when scanner-declared has logged at least
# one scanner this session; otherwise refuse, saying what to declare.
#
# Logic fails closed (no scanner logged → refuse); plumbing fails open (the
# registry cannot be read → permit, said on stderr), because a registry read
# that breaks would otherwise block every search for a reason the agent cannot
# fix — and verify-scanner-coverage still judges the turn at Stop.
set -uo pipefail

cat >/dev/null

if ! entries="$(sr-session state list --owner scanner-declared 2>/dev/null)"; then
  echo "search-needs-declared-scanner: could not read scanner-declared's registry; not refusing on a read that failed" >&2
  exit 0
fi

declared="$(printf '%s' "$entries" | jq -s '[.[] | select(.key | startswith("scanner:"))] | length' 2>/dev/null)"
if [ -z "$declared" ]; then
  echo "search-needs-declared-scanner: scanner-declared's registry did not parse; not refusing on it" >&2
  exit 0
fi

if [ "$declared" -gt 0 ]; then
  exit 0
fi

# Near misses: a file the agent evidently meant as a scanner, at a path no
# scanner is read from. Measured on a real unprimed run: the agent wrote
# `.sloprail/scanners/auth-token-logs.yaml`, was refused with the generic text
# three times, rewrote the same wrong file, and gave up on searching. Naming the
# file and the exact path it must be at is what the generic text lacked.
near_misses=""
unregistered=""
if [ -n "${SR_WORKSPACE:-}" ] && [ -d "$SR_WORKSPACE" ]; then
  while IFS= read -r p; do
    [ -n "$p" ] || continue
    rel="${p#"$SR_WORKSPACE"/}"
    if printf '%s' "$rel" | grep -qE '(^|/)scanners/[^/]+/scanner\.yaml$'; then
      unregistered="$unregistered $rel"
    else
      near_misses="$near_misses $rel"
    fi
  done <<EOF
$(find "$SR_WORKSPACE" -maxdepth 6 -path "$SR_WORKSPACE/.git" -prune -o -type f \( -iname '*scanner*.y*ml' -o -ipath '*scanners/*.y*ml' \) -print 2>/dev/null | head -20)
EOF
fi

hint=""
if [ -n "$near_misses" ]; then
  hint="$hint Not a scanner:${near_misses} — a scanner is read ONLY from a file named exactly scanner.yaml inside its own folder under scanners/, e.g. scanners/token-leaks/scanner.yaml (not a <name>.yaml, and not under .sloprail/). Write it there."
fi
if [ -n "$unregistered" ]; then
  hint="$hint${unregistered} is in the right place but was not registered in this session (it predates the session, was written by a shell command, or by another agent): write it again with the Write tool — unchanged is fine."
fi

jq -n --arg hint "$hint" '{reason: (
  "No scanner is declared in this session, and GitHub searches in this project run against one."
  + $hint + " "
  + "Declare one with the Write tool at scanners/<short-name>/scanner.yaml — for example scanners/token-leaks/scanner.yaml — containing:\n"
  + "  active: true\n  keywords:\n    - <keyword>\n    - <keyword>\n"
  + "naming every keyword this research must cover, then run ONE gh search whose query contains all of them "
  + "(e.g. gh search issues \"<keyword> <keyword>\"); narrower searches besides it are fine. "
  + "Reading a result you already found (gh issue view, gh repo view, gh api repos/...) needs no scanner."
)}'
exit 1
