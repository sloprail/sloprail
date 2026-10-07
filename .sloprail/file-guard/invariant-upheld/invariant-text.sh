#!/usr/bin/env bash
# prepare: hand the judge the predicate of the invariant this subject names, and the files
# marked sr:invariant <id> that this change touched (content and diff), so the judge rules on
# it without reading anything itself. A judge runs with this rule's folder as its working
# directory; a spec at the repository root is outside it, and asking the judge to fetch it
# costs a round of permission denials before it finds a way in.
#
# Copied from examples/business-invariants pinned-text.sh: the marker names
# spec/<domain>/invariants/<id>.yaml instead of a pinned spec line. A deleted file is skipped:
# it holds no code left to uphold anything (a marker leaving is invariant-covered's).
set -uo pipefail
payload="$(cat)"
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/changeset.sh"
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/spec.sh"

[ "$(cs '.event.kind // ""')" = "Changeset" ] \
  || refuse "The check payload is not a Changeset, so the invariant could not be read for the judge."

load_spec invariants; inv="$SPEC"
id="$(subject_id)"
[ -n "$id" ] || refuse "This rule judges one invariant per subject (subjects.sh), but the check was handed none."
doc="$(jq -c --arg id "$id" '[.[] | select(.id == $id)][0] // empty' <<<"$inv")"
# Removed or malformed: invariant-covered's and spec-quality's finding, nothing to judge here.
if [ -z "$doc" ]; then printf '{"skip": true}\n'; exit 0; fi
path="$(jq -r '.path' <<<"$doc")"
text="$(jq -r '.doc.predicate // empty' <<<"$doc")"
[ -n "$text" ] || refuse "$path has no predicate the judge could be given."
current="$(cat "$SR_TREE/$path")"

files="$(printf '%s' "$payload" | jq -c --arg id "$id" '[.changeset.files[]
  | select(.status != "D")
  | select(any((.newMarkers // [])[]; .kind == "invariant" and .fqn == $id))
  | {path, diff: (.diff // ""), newContent: (.newContent // "")}]')" \
  || refuse "The check payload did not parse, so the marked code could not be read for the judge."

# Only the invariant's spec changed: no marked code in this change to judge against it yet.
# The marked code at head is still what the judge must hold to the new wording, so hand it over.
if [ "$(jq 'length' <<<"$files")" -eq 0 ]; then
  load_markers invariant
  files="$(printf '%s\n' "$MARKERS" | awk -F'\t' -v id="$id" '$2 == id {print $1}' | sort -u | while IFS= read -r f; do
    [ -n "$f" ] && jq -n -c --arg p "$f" --rawfile c "$SR_TREE/$f" '{path: $p, diff: "", newContent: $c}'
  done | jq -s -c .)"
fi

jq -n -c --arg id "$id" --arg path "$path" --arg text "$text" --arg current "$current" --argjson files "$files" \
  '{additionalContext: {pins: [{fqn: $id, path: $path, text: $text, current: $current}], files: $files}}'
