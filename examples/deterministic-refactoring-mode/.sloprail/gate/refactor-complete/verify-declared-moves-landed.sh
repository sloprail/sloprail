#!/usr/bin/env bash
# The completeness check: every move the agent DECLARED must have LANDED. Run at
# Stop, only while the refactoring context is active (the gate's own match saw to
# that). This is where the refusal lives — a context's exit cannot block a Stop,
# a gate can.
#
# Receives GateCheckPayload on stdin. The declared scope is the paired context's
# payload, reached at `.context.refactoring.payload.declared_markers` — the
# refactoring context's enter wrote it there, and the gate's
# `require: [{context: refactoring}]` guarantees that context is active and its
# state settled before this runs. (This is the sanctioned in-payload channel, not
# `sr-session state list --owner`: this context keeps its whole declaration in the
# payload rather than growing a cross-tool registry, so reading the payload off
# stdin is the direct read — cf. completeness-artifact, which DOES use --owner
# because its context accumulates entries across separate tool calls.)
#
# THE CORRESPONDENCE (bug-3 design). Each declared token IS the fqn a landed
# `sr:moved-from` marker carries: `<path>@<sha>:<start>-<end>`. A completed move
# writes `// sr:moved-from <path>@<sha>:<lines>` into the destination file (and
# the preventive file-guard has already refused it unless its body reconciled
# against that origin). So "did this declared move land" is answered by a literal
# search of the workspace for a file carrying `sr:moved-from <fqn>` — no
# logical-nickname-to-marker mapping to guess. The marker convention is unchanged
# (kind stays `moved-from`); the declaration simply names the fqns up front.
#
# Portability: no `mapfile`/`readarray` (bash 3.2 on macOS lacks them) and no
# `${arr[@]}` under `set -u` — the declared set and the missing set are carried as
# newline-delimited strings walked with `while read`.
set -uo pipefail

input="$(cat)"

# The declared fqn set, one per line. jq emits nothing when the path is absent, so
# an empty string here means "nothing declared" -> permit (defensive: the gate's
# match should not fire without an active context carrying declared_markers, but a
# missing scope must not become a false refusal).
declared="$(printf '%s' "$input" \
  | jq -r '.context.refactoring.payload.declared_markers[]? | select(. != "")' 2>/dev/null)"

if [ -z "$declared" ]; then
  exit 0
fi

# Where the moved files live. SR_WORKSPACE is the tree being guarded; fall back to
# cwd only if it is somehow unset (a hook always sets it).
root="${SR_WORKSPACE:-.}"

# For each declared move, a file in the tree must carry its `sr:moved-from`
# marker. grep -F: the fqn is a literal (it contains '.', '@', ':', '-'), so a
# fixed-string search cannot misread it as a pattern. Leader-agnostic — the
# marker's `//`, `#` or `--` prefix is not part of what we match. .git is skipped
# so a stale blob cannot count as a landed move.
missing=""
while IFS= read -r fqn; do
  [ -z "$fqn" ] && continue
  if ! grep -rIF --exclude-dir=.git -- "sr:moved-from $fqn" "$root" >/dev/null 2>&1; then
    if [ -z "$missing" ]; then
      missing="$fqn"
    else
      missing="$missing, $fqn"
    fi
  fi
done <<EOF
$declared
EOF

if [ -n "$missing" ]; then
  # A {"reason": ...} object on stdout is the refusal contract the engine reads
  # (it prefers a structured stdout reason over anything on stderr) — the clean
  # post-migration shape, not the old decision-wrapped one.
  jq -n --arg detail "$missing" \
    '{reason: ("Refactor declared but not complete — these declared moves never landed as an sr:moved-from marker: " + $detail + ". Finish the moves you declared, or the turn cannot end.")}'
  exit 1
fi

# Every declared move landed (and, by the file-guard, reconciled) — permit.
exit 0
