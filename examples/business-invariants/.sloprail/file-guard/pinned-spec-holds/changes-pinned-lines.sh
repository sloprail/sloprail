#!/usr/bin/env bash
# `when` for the user citation a pinned rule needs. Exit 0 — this write changes
# what some sr:invariant marker pins, so it must cite the user's words asking for
# the rule to change. Exit 1 — it does not. Two ways a write changes a pinned rule:
#
#   1. It changes a spec line a marker pins (or deletes the spec, or puts a new
#      file where HEAD had one).
#   2. It moves or removes a marker so the code stops being pinned to the wording
#      it was pinned to: the marker dropped, or re-pinned to different text. A
#      re-pin to the SAME text (the rule moved down a line) changes nothing.
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 does not permit anything — it
# APPLIES the requirement. So every path this script cannot decide exits 0, the
# fail-closed direction; only exit 1 waives the citation, and only on a decided
# "changes nothing pinned".
#
# The failure it exists for, measured on two real Haiku runs: asked for a feature
# that breaks a pinned invariant, the agent rewrote the spec's rule to allow it and
# re-pinned its code to the new wording, so the code-upholds-invariant judge found
# code and pin agreeing — the rule weakened to match the work.
set -uo pipefail

command -v jq >/dev/null 2>&1 || exit 0
command -v git >/dev/null 2>&1 || exit 0

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

path="$(field '.event.path // ""')"
[ -n "$path" ] || exit 0
workspace="${SR_WORKSPACE:-.}"
kind="$(field '.event.kind // ""')"

case "$kind" in
  PreFileCreate | PreFileUpdate | PostFileCreate | PostFileUpdate | PreFileDelete | PostFileDelete) ;;
  *) exit 0 ;;
esac

# Which lines are pinned is read from git; outside a repository it cannot be.
git -C "$workspace" rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 0
ws_abs="$(cd "$workspace" 2>/dev/null && pwd -P)" || exit 0
has_head=0
git -C "$workspace" rev-parse -q --verify HEAD >/dev/null 2>&1 && has_head=1

# The engine's marker grammar (internal/filemod/marker.go): the whole line is the
# marker, after a `//`, `#` or `--` leader and any whitespace; the fqn is quoted or
# a bare token.
MARKER_RE='^[[:space:]]*(//|#|--)[[:space:]]*sr:invariant[[:space:]]+("[^"]*"|[^[:space:]"][^[:space:]]*)[[:space:]]*$'
fqns_in() {
  grep -E "$MARKER_RE" | sed -E 's#^[[:space:]]*(//|\#|--)[[:space:]]*sr:invariant[[:space:]]+##; s/[[:space:]]*$//; s/^"(.*)"$/\1/'
}

# norm <path>: repository-relative, with `./`, `//`, `.` and `..` resolved, so a pin
# written `./SPEC.md` is the file the event calls `SPEC.md`.
norm() {
  local p="$1" seg IFS=/
  local -a segs out=()
  case "$p" in
    "$ws_abs"/*) p="${p#"$ws_abs"/}" ;;
    "$workspace"/*) p="${p#"$workspace"/}" ;;
    /*) printf '%s' "$p"; return ;;
  esac
  read -r -a segs <<<"$p"
  for seg in ${segs[@]+"${segs[@]}"}; do
    case "$seg" in
      '' | .) ;;
      ..) if [ ${#out[@]} -gt 0 ]; then unset "out[$((${#out[@]} - 1))]"; else out+=(..); fi ;;
      *) out+=("$seg") ;;
    esac
  done
  printf '%s' "${out[*]+"${out[*]}"}"
}

# parse <fqn>: <repo>@<sha>:<path>#L<start>-<end> into f_repo, f_sha, f_path
# (normalized), f_start, f_end. Returns 1 when the sha or range is not a real one.
parse() {
  local fqn="$1" rest range
  f_repo="${fqn%%@*}"
  rest="${fqn#*@}"
  f_sha="${rest%%:*}"
  rest="${rest#*:}"
  f_path="$(norm "${rest%%#*}")"
  range="${rest#*#L}"
  f_start="${range%-*}"
  f_end="${range#*-}"
  printf '%s' "$f_sha" | grep -Eq '^[0-9a-f]{7,64}$' || return 1
  printf '%s' "$range" | grep -Eq '^[0-9]{1,9}-[0-9]{1,9}$' || return 1
  [ "$f_start" -ge 1 ] && [ "$f_start" -le "$f_end" ]
}

lines() { printf '%s\n' "$1" | sed -n "${2},${3}p"; }

# pinned_text <fqn>: the text the pin names, read at its own sha. Returns 1 when
# it cannot be read, or the range runs past the file or holds no text.
pinned_text() {
  local blob total text
  parse "$1" || return 1
  blob="$(git -C "$f_repo" cat-file blob "$f_sha:$f_path" 2>/dev/null)" || return 1
  total="$(printf '%s\n' "$blob" | awk 'END { print NR }')"
  [ "$total" -ge "$f_end" ] || return 1
  text="$(lines "$blob" "$f_start" "$f_end")"
  [ -n "$(printf '%s' "$text" | tr -d '[:space:]')" ] || return 1
  printf '%s\n%s' "$f_path" "$text"
}

# apply <what>: the requirement applies. `hint` is what the refusal carries (what
# the change does, then what to do); `what` alone is for the judge, which
# only-when-pinned.sh hands it. The engine reads `hint` and ignores the rest.
apply() {
  jq -n --arg what "$1" --arg remedy "$remedy" '{hint: ($what + " " + $remedy), what: $what}'
  exit 0
}

npath="$(norm "$path")"
case "$kind" in
  *Delete) how="sr-file delete $path --cite:user '<their exact words asking for the rule to change>'" ;;
  *Create) how="sr-file write $path --content '<the whole file>' --cite:user '<their exact words asking for the rule to change>'" ;;
  *) how="sr-file edit $path --old-string '<old>' --new-string '<new>' --cite:user '<their exact words asking for the rule to change>'" ;;
esac
remedy="Change a pinned rule only when the user asked for the rule itself to change, citing their words: $how. If what you were asked for conflicts with the rule, keep the rule and tell the user about the conflict instead."

# What the file held before this write. A create had nothing on disk, but HEAD may
# hold the path: `git mv SPEC.md SPEC.old` is not seen as a delete, and the Write
# that follows is a create of a file HEAD still has.
new_known=1
case "$kind" in
  Pre*Create | Pre*Update) [ "$(field '.event.resultKnown // false')" = "true" ] || new_known=0 ;;
esac
had_old=1
case "$kind" in
  *Create)
    old=""
    had_old=0
    if [ "$has_head" = 1 ] && old="$(git -C "$workspace" cat-file blob "HEAD:$npath" 2>/dev/null)"; then
      had_old=1
    fi
    old_fqns="$(printf '%s\n' "$old" | fqns_in)"
    ;;
  *)
    old="$(field '.event.oldContent // ""')"
    old_fqns="$(field '[(.event.oldMarkers // [])[] | select(.kind == "invariant") | .fqn] | .[]')"
    ;;
esac
new=""
new_fqns=""
case "$kind" in
  *Delete) ;;
  *)
    if [ "$new_known" = 1 ]; then
      new="$(field '.event.newContent // ""')"
      new_fqns="$(field '[(.event.newMarkers // [])[] | select(.kind == "invariant") | .fqn] | .[]')"
    fi
    ;;
esac

# 1. Spec lines. Every marker in the project, in the working tree (tracked or not)
# AND at HEAD, and in what this file held: a marker dropped or moved in the working
# tree first must not unpin the rule it pinned at HEAD.
tree="$(git -C "$workspace" grep --untracked -h -I -E "$MARKER_RE" 2>/dev/null)"
[ $? -le 1 ] || exit 0
committed=""
if [ "$has_head" = 1 ]; then
  committed="$(git -C "$workspace" grep -h -I -E "$MARKER_RE" HEAD 2>/dev/null)"
  [ $? -le 1 ] || exit 0
fi
all_fqns="$({ printf '%s\n' "$tree" "$committed"; printf '%s\n' "$old"; } | fqns_in | sort -u)"

# A pin this cannot decide applies the citation, but only after every other pin
# was compared: when a real pinned line changed, the refusal says so, rather than
# naming an unreadable marker (a placeholder in a skill's example, say).
changed=""
undecided=""
while IFS= read -r fqn; do
  [ -n "$fqn" ] || continue
  parse "$fqn"
  valid=$?
  [ "$f_path" = "$npath" ] || continue
  if [ "$valid" != 0 ]; then
    : "${undecided:=This change touches $path, which the sr:invariant marker '$fqn' pins with a sha or line range that is not a real one, so which lines it pins cannot be told.}"
    continue
  fi
  [ "$new_known" = 1 ] || apply "This change touches $path, which code in this project pins as a business rule (L$f_start-$f_end), and what it would leave cannot be worked out before it runs."
  if [ "$had_old" = 1 ]; then
    before="$(lines "$old" "$f_start" "$f_end")"
  else
    # Created where HEAD has nothing: what it must still say is the pinned text.
    if ! blob="$(git -C "$workspace" cat-file blob "$f_sha:$f_path" 2>/dev/null)"; then
      : "${undecided:=This change creates $path, which code in this project pins as a business rule (L$f_start-$f_end), and the pinned text could not be read to compare.}"
      continue
    fi
    before="$(lines "$blob" "$f_start" "$f_end")"
  fi
  after="$(lines "$new" "$f_start" "$f_end")"
  if [ "$before" != "$after" ]; then
    case ", $changed, " in
      *", L$f_start-$f_end, "*) ;;
      *) changed="${changed:+$changed, }L${f_start}-${f_end}" ;;
    esac
  fi
done <<EOF
$all_fqns
EOF

# 2. This file's own pins. A pin the file held that it no longer holds — not the
# same fqn, and no remaining pin to the same text — moves the code off the wording
# it answered to.
moved=""
if [ -n "$old_fqns" ]; then
  [ "$new_known" = 1 ] || apply "This change touches $path, which carries sr:invariant markers, and what it would leave of them cannot be worked out before it runs."
  new_texts=""
  while IFS= read -r nfqn; do
    [ -n "$nfqn" ] || continue
    t="$(pinned_text "$nfqn")" && new_texts="$new_texts$t"$'\n\x1e\n'
  done <<EOF
$new_fqns
EOF
  while IFS= read -r ofqn; do
    [ -n "$ofqn" ] || continue
    printf '%s\n' "$new_fqns" | grep -Fxq -- "$ofqn" && continue
    said=""
    if otext="$(pinned_text "$ofqn")"; then
      case "$new_texts" in
        "$otext"$'\n\x1e\n'* | *$'\n\x1e\n'"$otext"$'\n\x1e\n'*) continue ;;
      esac
      said=" (\"${otext#*$'\n'}\")"
    fi
    moved="${moved:+$moved, }'$ofqn'$said"
  done <<EOF
$old_fqns
EOF
fi

if [ -z "$changed$moved" ]; then
  [ -n "$undecided" ] && apply "$undecided"
  exit 1
fi

# It applies. The hint the refusal carries: a pinned rule is the user's decision.
what=""
[ -n "$changed" ] && what="This change rewrites $path $changed, which code in this project pins as a business rule. "
[ -n "$moved" ] && what="${what}This change moves $path off the spec wording its sr:invariant pin $moved named (the pin is removed, or re-pinned to different text), so the code would stop answering to that rule as written. "
apply "${what% }"
