#!/usr/bin/env bash
# Shared helpers for the commit-based checks. Source it; do not run it.
#
# A check reads ONE payload on stdin. Capture it into $payload before sourcing:
#
#   payload="$(cat)"
#   . "$SR_GUARDRAIL_DIR/../../_lib/changeset.sh"
#
# Payload shape (the Changeset event, see the design note):
#   .event.kind == "Changeset"
#   .changeset.{base,head,commits[],files[],others[],citations[]}
#   .changeset.files[] = {path,status,oldPath,oldContent,newContent,oldMarkers,newMarkers,diff}
#   .subject = {id, files[], context{}}   (only when the rule declares subjects:)
# Checks read the committed tree at $SR_TREE, never the working tree.

# cs JQ — a raw jq read of the payload.
cs() { printf '%s' "$payload" | jq -r "$1"; }

# cs_json JQ — a compact JSON read of the payload.
cs_json() { printf '%s' "$payload" | jq -c "$1"; }

# refuse MESSAGE — the refusal contract: {"reason"} on stdout, exit 1.
refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

# slim_payload [KEEP_REGEX] — drops every file's diff and contents from $payload except those of the
# paths matching KEEP_REGEX (a jq regex). A `subjects:` script runs in `verify` on every Stop and
# push and a payload can be tens of megabytes: one jq pass here, and every `cs` after it reads a
# few kilobytes instead of parsing the whole thing again.
slim_payload() {
  payload="$(printf '%s' "$payload" | jq -c --arg keep "${1:-^\\b\\B}" '.changeset.files |= map(if (.path | test($keep)) then . else del(.diff, .oldContent, .newContent) end)')"
}

# subject_id — the id of the subject this check was handed: what its rule's `subjects:` script
# named ("" when the rule has none, or when the subject is the engine's default whole changeset).
# A check of a split rule judges that subject alone; see subjects.sh.
subject_id() { local s; s="$(cs '.subject.id // ""')"; [ "$s" = changeset ] && s=""; printf '%s' "$s"; }

# want_subject ID — ID is the subject this check judges (or the check is not split).
want_subject() { local s; s="$(subject_id)"; [ -z "$s" ] || [ "$s" = "$1" ]; }

# The committed tree this check judges, checked once, here, at the top level.
# A check that cannot see the commit must not read the working tree in its
# place: fail closed. (A refuse inside $(…) would only exit the subshell and
# leave its JSON in a variable, so no helper below refuses from inside one:
# they set variables instead.)
[ -n "${SR_TREE:-}" ] && [ -d "${SR_TREE:-}" ] ||
  refuse "SR_TREE is not set, so the committed tree cannot be read; this rule only judges commits"
tree() { printf '%s' "$SR_TREE"; }

# load_markers KIND — sets MARKERS to every `sr:<KIND> <fqn>` marker in the
# committed tree, one "path<TAB>fqn" per line. Uses the engine's marker grammar:
# the whole line is the marker, after a //, # or -- leader; the fqn is a quoted
# or bare token. git grep exits 1 on no match (fine) and >1 on an error (refuse).
load_markers() {
  local kind="$1" out rc
  MARKERS=""
  out="$(git -C "$SR_TREE" grep -n -I -E \
    "^[[:space:]]*(//|#|--)[[:space:]]*sr:${kind}[[:space:]]+(\"[^\"]*\"|[^[:space:]\"][^[:space:]]*)[[:space:]]*$" \
    -- . ':!proposals/**' 2>&1)"
  rc=$?
  [ "$rc" -le 1 ] || refuse "could not search the committed tree for sr:${kind} markers: $out"
  [ -n "$out" ] || return 0
  MARKERS="$(printf '%s\n' "$out" | sed -E \
    "s#^([^:]+):[0-9]+:[[:space:]]*(//|\#|--)[[:space:]]*sr:${kind}[[:space:]]+\"?([^\"[:space:]]+)\"?[[:space:]]*\$#\1\t\3#")"
}

# load_yaml FILE — sets YAML to a committed-tree YAML file as JSON ("null" if absent).
load_yaml() {
  local f="$SR_TREE/$1"
  YAML=null
  [ -f "$f" ] || return 0
  YAML="$(yq -o=json '.' "$f" 2>/dev/null)" || refuse "$1 is not valid YAML"
}

# yaml_str_json STRING — YAML text (e.g. a file's oldContent) as JSON.
yaml_str_json() {
  [ -n "$1" ] || { printf 'null'; return 0; }
  printf '%s' "$1" | yq -o=json '.' 2>/dev/null || printf 'null'
}


# kebab ID — a spec, ADR, run or harness name: lowercase kebab, starting with a letter.
kebab() { printf '%s' "$1" | grep -Eq '^[a-z][a-z0-9]*(-[a-z0-9]+)*$'; }

# added_lines — every line this changeset adds, one "path<TAB>line<TAB>text" per
# line, parsed from the unified diffs (new-file line numbers). Moved code counts:
# it is added at its new path. Deleted files add nothing.
added_lines() {
  cs '.changeset.files[] | select(.status != "D") | ("\u0001" + .path), (.diff // "")' | awk '
    /^\001/ { p = substr($0, 2); next }
    /^@@/ { match($0, /\+[0-9]+/); n = substr($0, RSTART + 1, RLENGTH - 1) + 0; next }
    /^\+\+\+/ || /^---/ { next }
    /^\+/ { print p "\t" n "\t" substr($0, 2); n++; next }
    /^-/   { next }
    { n++ }'
}
