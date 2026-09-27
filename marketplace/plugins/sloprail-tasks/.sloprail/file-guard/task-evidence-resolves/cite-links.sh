# The ARTIFACT citation library, sourced by task-evidence-resolves (check-task.sh)
# and task-review (review-preflight.sh, expand-evidence.sh).
#
# An ARTIFACT is a frontmatter string `<repo-relative-file>:<ranges>` — where the
# result of the work is, at the lines that changed — resolved against the WORKING
# TREE. It is the one kind of citation a TASK.md still carries in its own bytes,
# because it names repository content, which reads the same on every checkout.
#
# What a task no longer carries: the user's words for the ask, and the tool output
# proving the work happened. Both are transcript content, and a transcript path
# resolves on no other machine, so both ride on the WRITE instead — `sr-file …
# --cite:user` / `--cite:tool_result` — and reach a check as `.event.citations`,
# already resolved by the session. Nothing here reads a transcript.
#
# Nothing here exits. These are functions that print and return, so the calling
# guardrail decides what a failure means. This file reads no event and dispatches on
# no kind — it is a pure library.

# citation_path "<citation>"  ->  the path half of `<path>:<ranges>`. `%%:*` removes
# the longest `:*` from the right, leaving the path; task.cue's _artifact regex
# admits no colon in the path, so there is one colon.
citation_path() { printf '%s' "${1%%:*}"; }

# citation_ranges "<citation>"  ->  the ranges half.
citation_ranges() { printf '%s' "${1##*:}"; }

# citation_lines "<ranges>"  ->  every line number the ranges name, one per line. A
# reversed range (`60-40`) yields nothing, which the resolver catches by testing for
# an empty expansion. `printf '%s\n'` WITH the trailing newline is not cosmetic:
# without it `read` discards the last range at EOF and every single range looks
# reversed (measured in the old citations.sh).
citation_lines() {
  printf '%s\n' "$1" | tr ',' '\n' | while IFS= read -r part; do
    [ -n "$part" ] || continue
    case "$part" in
      *-*)
        lo="${part%%-*}"; hi="${part##*-}"
        # BSD seq counts DOWN on a reversed range (`seq 5 2` prints 5 4 3 2 on
        # macOS, nothing on GNU), so a reversed range is refused HERE rather than
        # left to seq — otherwise the rule would behave differently on CI.
        [ "$lo" -le "$hi" ] 2>/dev/null || continue
        seq "$lo" "$hi" 2>/dev/null
        ;;
      *)   printf '%s\n' "$part" ;;
    esac
  done
}

# artifact_resolve "<artifact>" "<root>"  ->  0 if the repo-relative citation
# resolves against the tree (file exists under $root, every cited line exists),
# non-zero with a reason otherwise.
#
# It refuses the two shapes that mean the citation was mis-filed:
#   - an ABSOLUTE path — an artifact must be reviewable from any checkout;
#   - a .jsonl path — a session transcript is not a produced result. Proof that the
#     work happened is tool output, cited on the in_review write with
#     --cite:tool_result, never a path in the file.
# Both are named as such so the agent fixes the kind of evidence rather than a
# path. The remaining failures — wrong path, reversed range, range past the end —
# are kept apart because the fixes differ.
artifact_resolve() {
  citation="$1"
  root="$2"
  path="$(citation_path "$citation")"
  ranges="$(citation_ranges "$citation")"

  case "$path" in
    /*) printf 'the artifact %s is an ABSOLUTE path — an artifact is a repo-relative tree file, so it reads the same on any checkout\n' "$citation"
        return 1 ;;
  esac
  case "$path" in
    *.jsonl) printf 'the artifact %s points at a .jsonl transcript — that is not a produced result. Cite the tool output that proves the work on the in_review write itself (sr-file … --cite:tool_result '"'"'<exact output>'"'"'), and list only produced files under artifacts.\n' "$citation"
             return 1 ;;
  esac

  abs="$root/$path"
  if [ ! -f "$abs" ] || [ ! -r "$abs" ]; then
    printf 'the artifact names a repo file that is not in the tree: %s\n' "$path"
    return 1
  fi

  lines="$(citation_lines "$ranges")"
  if [ -z "$lines" ]; then
    printf 'the artifact %s names no lines (a reversed range, e.g. 60-40?)\n' "$citation"
    return 1
  fi

  # `wc -l` counts NEWLINES, so a last line with no trailing newline is undercounted
  # by one. The highest cited line is compared against the count and, only if it
  # looks past the end, actually read with sed — a line sed can produce exists
  # whatever wc said.
  total="$(wc -l < "$abs" | tr -d ' ')"
  for n in $lines; do
    [ "$n" -le "$total" ] 2>/dev/null && continue
    if [ -z "$(sed -n "${n}p" "$abs" 2>/dev/null)" ]; then
      printf 'the artifact %s cites line %s, past the end of the file (%s lines)\n' \
        "$path" "$n" "$total"
      return 1
    fi
  done
  return 0
}
