#!/usr/bin/env bash
# `when` for the user citation a pinned rule needs — the PRE-WRITE entry: it reads the
# Pre* events (the gate's), the working tree and HEAD. The file-guard's entry reads the
# committed Changeset. What counts as a pinned change, and why, is
# changes-pinned-lines-lib.sh's, beside the file-guard, which both entries source.
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 APPLIES the requirement, and every
# path this script cannot decide exits 0. Only a decided "changes nothing pinned" exits
# 1, with the `{"waived": …}` sentinel refuse-unknown-result.sh looks for.
lib_dir="$(cd "$(dirname "$0")/../../file-guard/pinned-spec-holds" && pwd)"
unset changes_pinned_lines_lib_loaded
. "$lib_dir/changes-pinned-lines-lib.sh" || exit 0
[ "${changes_pinned_lines_lib_loaded:-}" = 1 ] || exit 0
lib_setup

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
  PreFileCreate | PreFileUpdate | PreFileDelete) ;;
  *) exit 0 ;;
esac

# The working tree (tracked or not) and HEAD.
lib_tree "${SR_WORKSPACE:-.}" HEAD --untracked
npath="$(norm "$path")"

new_known=1
# What the write leaves is unknown when the engine could not work its result out
# ahead (resultKnown false: a `sed -i`, a `>`, a `cp`, a `tee`, an sr-file line it
# could not resolve). Unknown is not a pass: a path something pins, or a file that
# carries markers, applies (lib_evaluate); a path nothing pins is still waived, as
# nothing is at stake.
case "$kind" in
  PreFileCreate | PreFileUpdate) [ "$known" = "true" ] || new_known=0 ;;
esac
case "$kind" in *Delete) new="" new_fqns="" ;; esac
[ "$new_known" = 1 ] || { new="" new_fqns=""; }

# What the file held before this write. A create had nothing on disk, but HEAD may
# hold the path: `git mv SPEC.md SPEC.old` is not seen as a delete, and the Write
# that follows is a create of a file HEAD still has.
had_old=1
emptied=""
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
      [ -n "$old" ] && emptied=1
    fi
    ;;
esac

# The pins this file answered to before the session's work: HEAD's — not the
# disk's, which holds this session's own edits, so a pin the agent wrote a moment
# ago and is now correcting is not a pin being dropped.
old_fqns=""
if [ "$has_head" = 1 ]; then
  old_fqns="$(git -C "$workspace" cat-file blob "HEAD:$npath" 2>/dev/null | fqns_in)"
fi

case "$kind" in
  *Delete) how="sr-file delete $path --cite:user '<their exact words asking for this change>'" ;;
  *Create) how="sr-file write $path --content '<the whole file>' --cite:user '<their exact words asking for this change>'" ;;
  *) how="sr-file edit $path --old-string '<old>' --new-string '<new>' --cite:user '<their exact words asking for this change>'" ;;
esac
lib_remedy

# When the result is unknown the change is most likely harmless — a rename by
# `sed -i` that keeps every pin — and needs no citation at all once it can be
# checked. So the first advice is to make it checkable; citing comes second.
unknown_result="what it would leave cannot be worked out before it runs (a shell command that edits it, or an sr-file call whose dry run failed — if sr-file said why, fix that first; run sr-file alone in its own Bash call, with nothing before or after it on the line, since another command beside it (sr-file ...; cat X) makes the result unknowable and the edit itself is allowed)"
unknown_remedy="Make this edit with Edit or Write, or with sr-file edit on its own in the command, so what it leaves can be checked first: no citation is needed when it keeps every pinned line and every pin. $remedy"

case "$kind" in
  PreFileCreate) op=create ;;
  PreFileUpdate) op=update ;;
  *) op=delete ;;
esac
lib_evaluate "$op" "$path" "$old" "$new" "$old_fqns" "$new_fqns"
lib_finish
