#!/usr/bin/env bash
# Helpers for a rule's `subjects:` script. Source after changeset.sh (and the other libs it needs).
#
# `subjects:` splits a rule's changeset into units, each judged and stored on its own key. The
# key of a subject is its files' content plus its `fingerprint`, and nothing else, so:
#
#   - `files` are changed files the rule SELECTED (the engine refuses any other): the part of the
#     change that is this unit's;
#   - `fingerprint` must cover EVERYTHING ELSE the unit's verdict reads: a file the check opens
#     from $SR_TREE that did not change, a base-side content, a list of siblings. A dependency
#     left out is a stale verdict served after it changes: worse than no split.
#
# The script runs in `run` and in `verify` alike, with no session, from the head tree.
# The rule's own checks then read `.subject.id` (subject_id / want_subject in changeset.sh) and
# judge that unit alone: the payload they get is still the whole changeset.
#
#   payload="$(cat)"; . changeset.sh; . subjects.sh
#   sub_finish FALLBACK_ID "$(jq ... one array of {id, files, deps, bdeps, extra})"

SUBJECTS="[]"

# These scripts run in `verify` (Stop, pre-push, CI) as well as `run`, so they are written for
# speed: work from the changeset payload, never walk history, and spawn a fixed number of
# processes whatever the number of units (one jq over the whole payload, one `git cat-file
# --batch-check` for every object id, one `git hash-object` for every fingerprint).
#
# A script builds ONE JSON array of {id, files, deps, bdeps, extra} and hands it to sub_finish:
#   deps   paths whose content at the range's head the verdict depends on (a file or a directory)
#   bdeps  paths whose content at the range's base it depends on
#   extra  any other text the verdict depends on
# The fingerprint is a hash of the extra text and each dep's object id.

# sub_oids REV — stdin: one path per line; stdout: the object id of each at REV ("-" if absent).
sub_oids() {
  local in out
  in="$(sed "s|^|$1:|")"
  out="$(printf '%s\n' "$in" | git -C "$SR_TREE" cat-file --batch-check 2>&1)" || refuse "could not read object ids at $1: $out"
  [ "$(printf '%s\n' "$in" | wc -l)" -eq "$(printf '%s\n' "$out" | wc -l)" ] || refuse "object id lookup at $1 returned the wrong number of lines"
  printf '%s\n' "$out" | awk '{ if ($NF == "missing") print "-"; else print $1 }'
}

# sub_resolve ARRAY — ARRAY with each subject's deps and bdeps resolved: [{id, files, text}]
sub_resolve() {
  local arr="$1" hp bp hm bm
  hp="$(jq -r '[.[].deps // [] | .[]] | unique | .[]' <<<"$arr")"
  bp="$(jq -r '[.[].bdeps // [] | .[]] | unique | .[]' <<<"$arr")"
  hm='{}'; bm='{}'
  [ -z "$hp" ] || hm="$(paste <(printf '%s\n' "$hp") <(printf '%s\n' "$hp" | sub_oids "$(cs '.changeset.head')") | jq -Rn '[inputs | split("\t") | {(.[0]): .[1]}] | add')"
  [ -z "$bp" ] || bm="$(paste <(printf '%s\n' "$bp") <(printf '%s\n' "$bp" | sub_oids "$(cs '.changeset.base')") | jq -Rn '[inputs | split("\t") | {(.[0]): .[1]}] | add')"
  jq -c --argjson h "$hm" --argjson b "$bm" '[.[] | {id, files: (.files | unique), text: ((.extra // "")
      + ((.deps // []) | map("\n" + . + "=" + $h[.]) | join(""))
      + ((.bdeps // []) | map("\nbase:" + . + "=" + $b[.]) | join("")))}]' <<<"$arr"
}

# sub_finish FALLBACK_ID ARRAY — prints the subjects of ARRAY (see above). The engine refuses an
# empty list for a rule that selected files, so when nothing claimed any (a file the rule selects
# but no unit owns) the whole selection becomes one subject named FALLBACK_ID, which the rule's
# checks find nothing to judge in.
sub_finish() {
  local arr n tmp i=0 line
  arr="$(sub_resolve "$2")"; n="$(jq 'length' <<<"$arr")"
  if [ "$n" -eq 0 ]; then
    cs_json '[{id: "'"$1"'", files: ([.changeset.files[].path] | unique)}]'
    return 0
  fi
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/sr-subjects.XXXXXX")"
  jq -c '.[] | .text | tojson' <<<"$arr" | while IFS= read -r line; do printf '%s\n' "$line" >"$tmp/$i"; i=$((i + 1)); done
  seq 0 $((n - 1)) | sed "s|^|$tmp/|" | git hash-object --stdin-paths |
    jq -Rn --argjson s "$arr" '[inputs] as $h | [range(0; $s | length) as $i | $s[$i] | {id, files} + (if .text == "" then {} else {fingerprint: $h[$i]} end)]'
  rm -rf "$tmp"
}

# changed_paths — every path the changeset selected, one per line.
changed_paths() { cs '.changeset.files[].path'; }
