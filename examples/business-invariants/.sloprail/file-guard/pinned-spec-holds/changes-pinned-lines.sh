#!/usr/bin/env bash
# `when` for the user citation a pinned rule needs. Exit 0 — this write changes
# a spec some sr:invariant marker pins, or what a marker pins, so it must cite the
# user's words asking for that change. Exit 1 — it does not. Three ways a write
# does:
#
#   1. It changes a spec line a marker pins (or deletes the spec, or puts a new
#      file where HEAD had one) — the rule itself changes.
#   2. It changes any other line of a spec some marker pins: a new rule, a
#      rewording, an exception on a line of its own. A pinned spec holds the
#      user's business rules, like an ask: an uncited "3. Goodwill refunds may
#      exceed the charge" beside a pinned "2. A refund must never exceed the
#      charge" is how a real run left the spec contradicting itself (231517Z).
#   3. It moves or removes a marker so the code stops being pinned to the wording
#      it was pinned to: the marker dropped, or re-pinned to different text. A
#      re-pin to the SAME text (the rule moved down a line) changes nothing, and
#      neither does a marker that leaves this file while another file carries it
#      (marked code moved: written with its marker in the new place first).
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 does not permit anything — it
# APPLIES the requirement. So every path this script cannot decide exits 0, the
# fail-closed direction. Only waive() exits 1, on a decided "changes nothing
# pinned", and prints a sentinel only-when-pinned.sh looks for; any other exit 1
# (a crash, a tool that failed) is turned into 0 by the trap below.
#
# The failure it exists for, measured on two real Haiku runs: asked for a feature
# that breaks a pinned invariant, the agent rewrote the spec's rule to allow it and
# re-pinned its code to the new wording, so the code-upholds-invariant judge found
# code and pin agreeing — the rule weakened to match the work.
#
# Bytes, not meaning: a pinned line changed only in whitespace or line endings is
# changed, as it is to pin-still-matches-head.sh, which compares the same bytes.
# Markers inside a git submodule are not seen (git grep does not enter one).
set -uo pipefail

# A replace ref (refs/replace/<sha>) would make git read another object in place
# of the one a pin names; pins name objects, so read the objects themselves.
export GIT_NO_REPLACE_OBJECTS=1

waived=""
trap 'rc=$?; if [ "$rc" = 1 ] && [ "$waived" != 1 ]; then exit 0; fi' EXIT
waive() {
  waived=1
  jq -n --arg why "$1" '{waived: $why}'
  exit 1
}

command -v jq >/dev/null 2>&1 || exit 0

# The payload, parsed once.
payload="$(cat)"
parsed="$(printf '%s' "$payload" | jq -r '
  .event as $e
  | @sh "kind=\($e.kind // "")",
    @sh "path=\($e.path // "")",
    @sh "known=\($e.resultKnown // false | tostring)",
    @sh "old=\($e.oldContent // "")",
    @sh "new=\($e.newContent // "")",
    @sh "old_fqns=\([($e.oldMarkers // [])[] | select(.kind == "invariant") | .fqn] | join("\n"))",
    @sh "new_fqns=\([($e.newMarkers // [])[] | select(.kind == "invariant") | .fqn] | join("\n"))"
' 2>/dev/null)" || exit 0
kind="" path="" known="" old="" new="" old_fqns="" new_fqns=""
eval "$parsed"
[ -n "$path" ] || exit 0

case "$kind" in
  PreFileCreate | PreFileUpdate | PostFileCreate | PostFileUpdate | PreFileDelete | PostFileDelete) ;;
  *) exit 0 ;;
esac

command -v git >/dev/null 2>&1 || exit 0
workspace="${SR_WORKSPACE:-.}"
# A pin names <repo>@<sha>: outside a git work tree nothing can be pinned, which
# is a decided answer, not an undecidable one.
git -C "$workspace" rev-parse --is-inside-work-tree >/dev/null 2>&1 || waive "not a git work tree"
ws_abs="$(cd "$workspace" 2>/dev/null && pwd -P)" || exit 0
has_head=0
git -C "$workspace" rev-parse -q --verify HEAD >/dev/null 2>&1 && has_head=1

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
npath="$(norm "$path")"

new_known=1
case "$kind" in
  PreFileCreate | PreFileUpdate) [ "$known" = "true" ] || new_known=0 ;;
esac
case "$kind" in *Delete) new="" new_fqns="" ;; esac
[ "$new_known" = 1 ] || { new="" new_fqns=""; }

# What the file held before this write. A create had nothing on disk, but HEAD may
# hold the path: `git mv SPEC.md SPEC.old` is not seen as a delete, and the Write
# that follows is a create of a file HEAD still has.
had_old=1
case "$kind" in
  *Create)
    old=""
    had_old=0
    if [ "$has_head" = 1 ] && old="$(git -C "$workspace" cat-file blob "HEAD:$npath" 2>/dev/null)"; then
      had_old=1
    fi
    ;;
  *Delete)
    # A delete whose bytes the engine did not read (an `rm -r` past its byte
    # budget, say) arrives with an empty oldContent, which would read as "the
    # pinned lines were already empty": unchanged. What a delete removes is at
    # least what HEAD holds, so an empty oldContent is read from HEAD instead.
    if [ -z "$old" ] && [ "$has_head" = 1 ]; then
      old="$(git -C "$workspace" cat-file blob "HEAD:$npath" 2>/dev/null)"
    fi
    ;;
esac

# The engine's marker grammar (internal/filemod/marker.go): the whole line is the
# marker, after a `//`, `#` or `--` leader and any whitespace; the fqn is quoted or
# a bare token.
MARKER_RE='^[[:space:]]*(//|#|--)[[:space:]]*sr:invariant[[:space:]]+("[^"]*"|[^[:space:]"][^[:space:]]*)[[:space:]]*$'
fqns_in() {
  grep -E "$MARKER_RE" | sed -E 's#^[[:space:]]*(//|\#|--)[[:space:]]*sr:invariant[[:space:]]+##; s/[[:space:]]*$//; s/^"(.*)"$/\1/'
}
# The pins this file answered to before the session's work: on a Post kind, the
# session baseline's (the event's oldMarkers); on a Pre kind, HEAD's — not the
# disk's, which holds this session's own edits, so a pin the agent wrote a moment
# ago and is now correcting is not a pin being dropped.
case "$kind" in
  Pre*)
    old_fqns=""
    if [ "$has_head" = 1 ]; then
      old_fqns="$(git -C "$workspace" cat-file blob "HEAD:$npath" 2>/dev/null | fqns_in)"
    fi
    ;;
esac

# The cheap answer first. A file that carries no marker, before or after, can
# only matter as a spec some marker pins — and every such marker names its path
# followed by `#L`. When no file in the working tree or at HEAD holds even the
# file's base name followed by `#L`, nothing pins it.
if [ -z "$old_fqns$new_fqns" ]; then
  needle="${npath##*/}#L"
  hit=0
  git -C "$workspace" grep --untracked -q -I -F -e "$needle" 2>/dev/null
  case $? in 0) hit=1 ;; 1) ;; *) exit 0 ;; esac
  if [ "$hit" = 0 ] && [ "$has_head" = 1 ]; then
    git -C "$workspace" grep -q -I -F -e "$needle" HEAD 2>/dev/null
    case $? in 0) hit=1 ;; 1) ;; *) exit 0 ;; esac
  fi
  [ "$hit" = 1 ] || waive "nothing pins $npath"
fi

# parse <fqn>: <repo>@<sha>:<path>#L<start>-<end> into f_repo, f_hex, f_path
# (normalized), f_start, f_end. Returns 1 only when the fqn does not have the
# shape of a pin at all — a sha that is not hex, a range that is not
# 1 <= start <= end (a placeholder in a skill's example, say). Such a marker
# cannot pass pinned-invariant and pins nothing. A pin that HAS the shape pins its
# lines whether or not its sha resolves: its range names lines of its path.
parse() {
  local fqn="$1" rest range
  f_repo="${fqn%%@*}"
  rest="${fqn#*@}"
  f_hex="${rest%%:*}"
  rest="${rest#*:}"
  f_path="$(norm "${rest%%#*}")"
  range="${rest#*#L}"
  f_start="${range%-*}"
  f_end="${range#*-}"
  [ "$f_repo" != "$fqn" ] || return 1
  printf '%s' "$f_hex" | grep -Eq '^[0-9a-f]{7,64}$' || return 1
  printf '%s' "$range" | grep -Eq '^[0-9]{1,9}-[0-9]{1,9}$' || return 1
  [ "$f_start" -ge 1 ] && [ "$f_start" -le "$f_end" ] || return 1
}

# A per-run memo, so a sha or a blob named by several pins is read from git once.
memo="$(mktemp -d "${TMPDIR:-/tmp}/pinned-spec-holds.XXXXXX")" || exit 0
trap 'rc=$?; rm -rf "$memo"; if [ "$rc" = 1 ] && [ "$waived" != 1 ]; then exit 0; fi' EXIT
memo_key() { printf '%s' "$*" | cksum | tr ' ' '-'; }

# resolve: sets f_commit to the one commit f_hex names in f_repo, by object id
# only — never through a ref, so a branch or tag named like the sha cannot stand
# in for it. Returns 1 when no commit, or more than one, has that prefix.
resolve() {
  local key
  key="$memo/sha-$(memo_key "$f_repo" "$f_hex")"
  if [ ! -e "$key" ]; then
    : >"$key"
    if printf '%s' "$f_hex" | grep -Eq '^([0-9a-f]{40}|[0-9a-f]{64})$'; then
      [ "$(git -C "$f_repo" cat-file -t "$f_hex" 2>/dev/null)" = commit ] && printf '%s' "$f_hex" >"$key"
    else
      git -C "$f_repo" rev-parse --disambiguate="$f_hex" 2>/dev/null | while IFS= read -r o; do
        [ "$(git -C "$f_repo" cat-file -t "$o" 2>/dev/null)" = commit ] && printf '%s\n' "$o"
      done >"$key.all"
      [ "$(grep -c . "$key.all")" = 1 ] && tr -d '\n' <"$key.all" >"$key"
    fi
  fi
  f_commit="$(cat "$key")"
  [ -n "$f_commit" ]
}

# blob_at <commit> <path>: the file at that commit, memoized.
blob_at() {
  local key
  key="$memo/blob-$(memo_key "$f_repo" "$1" "$2")"
  if [ ! -e "$key" ]; then
    git -C "$f_repo" cat-file blob "$1:$2" >"$key" 2>/dev/null || { rm -f "$key"; return 1; }
  fi
  cat "$key"
}

lines() { printf '%s\n' "$1" | sed -n "${2},${3}p"; }

# pinned_text <fqn>: the pin's path and the text it names, read at its own sha.
# Returns 1 when it cannot be read: the fqn is not a pin's shape, its sha names no
# single commit, or the range runs past the file or is blank.
pinned_text() {
  local blob total text
  parse "$1" || return 1
  resolve || return 1
  blob="$(blob_at "$f_commit" "$f_path")" || return 1
  total="$(printf '%s\n' "$blob" | awk 'END { print NR }')"
  [ "$total" -ge "$f_end" ] || return 1
  text="$(lines "$blob" "$f_start" "$f_end")"
  [ -n "$(printf '%s' "$text" | tr -d '[:space:]')" ] || return 1
  printf '%s\n%s' "$f_path" "$text"
}

case "$kind" in
  *Delete) how="sr-file delete $path --cite:user '<their exact words asking for this change>'" ;;
  *Create) how="sr-file write $path --content '<the whole file>' --cite:user '<their exact words asking for this change>'" ;;
  *) how="sr-file edit $path --old-string '<old>' --new-string '<new>' --cite:user '<their exact words asking for this change>'" ;;
esac
# What to do next, for every refusal. A cited change the judge refused is refused
# again when it is re-submitted with the same words: a real run (231517Z) sent the
# same refused `sr-file edit` twice before stopping.
remedy="If what you were asked for conflicts with the rule: Undo the code that breaks the rule. Do not reshape the requested feature to fit the rule (moving a credit before the check changes what the user asked for); keep the rule and tell the user the request conflicts with it and was not built — do not leave a flag that changes nothing, or describe a credit issued elsewhere that no code issues — and stop there; the rule changes only when the user asks for that. Only if the user asked for this change to the rule, cite their words: $how. A cited change that was refused is refused again if you send it again with the same words; do not retry it — tell the user instead."
spec_remedy="A spec that code pins holds the user's business rules: change it only when the user asked for that change — a new rule, a rewording, an exception — citing their words: $how. If they did not ask for it, leave the spec as it is and tell the user what you would change and why. A cited change that was refused is refused again if you send it again with the same words."

# apply <what>: the requirement applies. `hint` is what the refusal carries (what
# the change does, then what to do); `what` alone is for the judge, which
# only-when-pinned.sh hands it. The engine reads `hint` and ignores the rest.
apply() {
  jq -n --arg what "$1" --arg remedy "${2:-$remedy}" '{hint: ($what + " " + $remedy), what: $what}'
  exit 0
}

# When the result is unknown the change is most likely harmless — a rename by
# `sed -i` that keeps every pin — and needs no citation at all once it can be
# checked. So the first advice is to make it checkable; citing comes second.
unknown_result="what it would leave cannot be worked out before it runs (a shell command that edits it, or an sr-file call whose dry run failed — if sr-file said why, fix that first)"
unknown_remedy="Make this edit with Edit or Write, or with sr-file edit on its own in the command, so what it leaves can be checked first: no citation is needed when it keeps every pinned line and every pin. $remedy"

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

changed=""
pinned_here=""
while IFS= read -r fqn; do
  [ -n "$fqn" ] || continue
  parse "$fqn" || continue
  [ "$f_path" = "$npath" ] || continue
  case ", $pinned_here, " in
    *", L$f_start-$f_end, "*) ;;
    *) pinned_here="${pinned_here:+$pinned_here, }L${f_start}-${f_end}" ;;
  esac
  [ "$new_known" = 1 ] || apply "This change touches $path, which code in this project pins as a business rule (L$f_start-$f_end), and $unknown_result." "$unknown_remedy"
  if [ "$had_old" = 1 ]; then
    before="$(lines "$old" "$f_start" "$f_end")"
  else
    # Created where HEAD has nothing: what it must still say is the pinned text.
    # A pin whose sha names no single commit leaves that unknowable.
    if ! resolve || ! blob="$(blob_at "$f_commit" "$f_path")"; then
      apply "This change creates $path, which the sr:invariant pin '$fqn' pins (L$f_start-$f_end), and the pinned text could not be read to compare: its sha names no single commit in $f_repo."
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

# 2. The rest of a pinned spec. Any change to a file some pin names — a new rule,
# a rewording, an exception on a line of its own — is a change to the user's
# business rules. Whitespace outside the pinned lines is not: a formatter
# trimming trailing spaces, CRLF made LF, a trailing newline added or dropped
# changes no rule, so it is compared with that whitespace removed. The pinned
# lines themselves stay byte-exact (step 1), as pin-still-matches-head.sh
# compares them.
ws_norm() {
  printf '%s\n' "$1" | tr -d '\r' | sed -e 's/[[:space:]]*$//' |
    awk '{ l[NR] = $0 } END { n = NR; while (n > 0 && l[n] == "") n--; for (i = 1; i <= n; i++) print l[i] }'
}
spec_changed=0
if [ -n "$pinned_here" ] && [ -z "$changed" ]; then
  if [ "$had_old" = 0 ] || [ "$(ws_norm "$old")" != "$(ws_norm "$new")" ]; then
    spec_changed=1
  fi
fi

# 3. This file's own pins. A pin the file held stays held when this file still
# carries it, or another file in the working tree does (marked code moved), by
# the same fqn or by a pin to the same text; otherwise the code moves off the
# wording it answered to.
moved=""
if [ -n "$old_fqns" ]; then
  [ "$new_known" = 1 ] || apply "This change touches $path, which carries sr:invariant markers, and $unknown_result." "$unknown_remedy"
  elsewhere="$(git -C "$workspace" grep --untracked -h -I -E "$MARKER_RE" -- . ":(exclude,literal)$npath" 2>/dev/null)"
  [ $? -le 1 ] || exit 0
  # One command deleting two files that carry the same pin (`rm a.go b.go`) is
  # not caught here: the engine asks a preventive guard about the first file a
  # command touches and not again, and that file sees the other still holding
  # the pin. At Stop both files are gone, neither holds it, and both deletes are
  # refused — the after-check is the backstop.
  held="$({ printf '%s\n' "$new_fqns"; printf '%s\n' "$elsewhere" | fqns_in; } | sort -u)"
  held_texts=""
  while IFS= read -r hfqn; do
    [ -n "$hfqn" ] || continue
    t="$(pinned_text "$hfqn")" && held_texts="$held_texts$t"$'\n\x1e\n'
  done <<EOF
$held
EOF
  while IFS= read -r ofqn; do
    [ -n "$ofqn" ] || continue
    printf '%s\n' "$held" | grep -Fxq -- "$ofqn" && continue
    # A marker without a pin's shape pinned nothing (pinned-invariant refuses it),
    # so correcting or removing it drops nothing. One with the shape whose text
    # cannot be read is still dropped: what it pinned cannot be shown kept.
    parse "$ofqn" || continue
    if otext="$(pinned_text "$ofqn")"; then
      case "$held_texts" in
        "$otext"$'\n\x1e\n'* | *$'\n\x1e\n'"$otext"$'\n\x1e\n'*) continue ;;
      esac
      moved="${moved:+$moved, }'$ofqn' (\"${otext#*$'\n'}\")"
    else
      moved="${moved:+$moved, }'$ofqn' (its pinned text could not be read)"
    fi
  done <<EOF
$old_fqns
EOF
fi

if [ -z "$changed$moved" ]; then
  [ "$spec_changed" = 1 ] || waive "changes no pinned spec and keeps every pin"
  apply "This change edits $path, a spec that code in this project pins ($pinned_here). It leaves the pinned lines as they are, but every rule in a pinned spec is the user's." "$spec_remedy"
fi

# It applies. The hint the refusal carries: a pinned rule is the user's decision.
what=""
[ -n "$changed" ] && what="This change rewrites $path $changed, which code in this project pins as a business rule. "
[ -n "$moved" ] && what="${what}This change moves $path off the spec wording its sr:invariant pin $moved named (the pin is removed, or re-pinned to different text), so the code would stop answering to that rule as written. To move marked code rather than drop its pin, write it with its marker in the new place first, then remove it here. "
apply "${what% }"
