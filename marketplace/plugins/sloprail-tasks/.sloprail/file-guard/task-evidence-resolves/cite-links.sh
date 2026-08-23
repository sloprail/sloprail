# The citation library, sourced by every task guardrail that grounds a claim.
#
# This is the ONE place the task guardrails understand a citation, and it owns
# almost no logic of its own: two citation grammars, and calls to sr-session for the
# actual grounding. It walks NO transcript itself — cite (for the body's ask) and
# `tool-result` (for a delivery observation) do that, so the transcript reader lives
# in one place and every consumer gets its filters for free.
#
# THERE ARE THREE CITATION KINDS, and keeping them apart is the point of the whole
# review lifecycle — see the plugin README and task.cue. They differ in WHERE they
# live and WHAT resolves them:
#
#   1. A BODY citation of the ASK — an INLINE `[quote](jsonl)` markdown link in the
#      TASK.md body, grounded against the `user` pool. The human's own words for
#      what was asked. task-body-is-human-authored. (cite_links_extract / cite_ground)
#   2. An OBSERVATION of DELIVERY — a FRONTMATTER string `<abs-jsonl>:<ranges>`.
#      Proof a command ran and what it returned. Each cited LINE must be a
#      tool_result. task-review's observations. (observation_resolve)
#   3. An ARTIFACT of DELIVERY — a FRONTMATTER string `<repo-rel-file>:<ranges>`.
#      Where the produced result is, at the lines that changed, resolved against the
#      WORKING TREE. task-review's artifacts. (artifact_resolve)
#
# The two DELIVERY kinds (2, 3) are STRUCTURED FRONTMATTER, not body links, and they
# have DIFFERENT PATH BASES: an observation is an ABSOLUTE .jsonl (the transcript, a
# reviewer opens it by absolute path); an artifact is REPO-RELATIVE (a produced file,
# reviewable from any checkout). An observation is NEVER a repo file and NEVER sent
# through the tree checker; an artifact is NEVER a .jsonl and NEVER sent through
# cite/tool-result. That divergence is what tells each resolver which mechanism to
# use, and a citation wearing the other kind's shape is refused.
#
# Nothing here exits. These are functions that print and return, so the calling
# guardrail decides what a failure means. This file reads no event and dispatches on
# no kind — it is a pure library.

# =========================================================== BODY ask links ===
#
# The body's inline `[quote](jsonl)` citations of the human's ask. Grounded against
# the `user` pool. Used by task-body-is-human-authored (and nowhere else — the
# delivery evidence is frontmatter, below).

# cite_links_extract "<markdown text>"  ->  one `<jsonl-path>\t<quote>` per line, for
# every `[quote](jsonl-path)` link in the text.
#
# TAB-separated because a path cannot contain a tab and a quote is unlikely to; it
# lets a caller split cleanly with `cut`. A link whose href is not a .jsonl is
# skipped — a transcript citation grounds in the transcript. perl for the parse: the
# link text may carry punctuation a naive grep tears on, and perl's non-greedy
# capture handles several links on one line. Absent perl yields nothing (no links),
# which the caller reads as "no citation" and refuses — the safe direction.
cite_links_extract() {
  printf '%s' "$1" | perl -ne '
    while (/\[([^\]]+)\]\(([^)\s]+\.jsonl(?::[0-9]+)?)\)/g) {
      my ($quote, $href) = ($1, $2);
      print "$href\t$quote\n";
    }
  ' 2>/dev/null
}

# cite_link_href_path "<href>"  ->  the .jsonl path with any `:line` suffix stripped,
# so cite is handed the file path it wants (cite resolves the line itself).
cite_link_href_path() {
  case "$1" in
    *.jsonl:[0-9]*) printf '%s' "${1%:*}" ;;
    *)              printf '%s' "$1" ;;
  esac
}

# cite_ground "<pool>" "<jsonl-path>" "<quote>"  ->  0 if cite resolves the quote to
# that pool in that trajectory, non-zero with a reason otherwise.
#
# <pool> is `user` or `tool_result` — cite's own --source-types names, passed
# through. The body rule passes `user` (the ask is the human's words). The reason
# text is worded for the pool so a mis-citation names the right fix. cite MUST be
# given --path explicitly: with no --path it fails closed, so a bare `cite "$quote"`
# would refuse every citation.
#
#   0 grounded (silent)   1 no match   2 several   3 sub-agent   * cite failed
cite_ground() {
  pool="$1"
  cpath="$2"
  quote="$3"

  if [ ! -f "$cpath" ]; then
    printf 'the citation names a transcript that is not there: %s\n' "$cpath"
    return 1
  fi

  sr-session trajectory cite --source-types "$pool" --path "$cpath" "$quote" >/dev/null 2>&1
  rc=$?

  case "$pool" in
    tool_result) whatitis="a tool-call result (a command that ran and its output)" ;;
    *)           whatitis="the user's own words" ;;
  esac

  case "$rc" in
    0) return 0 ;;
    1) printf 'the quote "%s" resolves to nothing in the %s pool of %s — it is not %s. Quote the exact text that IS.\n' "$quote" "$pool" "$cpath" "$whatitis"
       return 1 ;;
    2) printf 'the quote "%s" matches SEVERAL entries in the %s pool of %s — extend it until it lands on exactly one.\n' "$quote" "$pool" "$cpath"
       return 1 ;;
    3) printf 'cite cannot ground "%s" here: this trajectory is a sub-agent'\''s, whose records are the parent'\''s dispatch, not the end user'\''s session.\n' "$quote"
       return 1 ;;
    *) printf 'could not verify the quote "%s" against %s (cite exited %s).\n' "$quote" "$cpath" "$rc"
       return 1 ;;
  esac
}

# ============================================ the two DELIVERY citation kinds ===
#
# Frontmatter `<path>:<ranges>` citation strings. Shared helpers to split one, then
# a resolver per kind. citation_path / citation_ranges / citation_lines are the
# common `<path>:<ranges>` split, used by BOTH kinds (the split is identical; only
# what the path MEANS and how the lines are checked differ).

# citation_path "<citation>"  ->  the path half of `<path>:<ranges>`. `%%:*` removes
# the longest `:*` from the right, leaving the path; neither an absolute path nor a
# repo-relative one carries a colon under task.cue's regexes, so there is one colon.
citation_path() { printf '%s' "${1%%:*}"; }

# citation_ranges "<citation>"  ->  the ranges half.
citation_ranges() { printf '%s' "${1##*:}"; }

# citation_lines "<ranges>"  ->  every line number the ranges name, one per line. A
# reversed range (`60-40`) yields nothing, which the resolvers catch by testing for
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

# ------------------------------------------------ OBSERVATION resolution ------
#
# An observation is `<abs-jsonl>:<ranges>` into the transcript. It resolves by
# confirming EVERY cited LINE is a tool_result the session produced — the check the
# old guardrail named as the missing piece. `sr-session trajectory tool-result
# --line N` is the authority (built on the same tool_result pool cite searches), and
# it is NEVER the tree checker: an observation is in the transcript, not the tree.

# observation_resolve "<observation>" "<root>"  ->  0 if every cited line of the
# absolute .jsonl is a tool_result, non-zero with a reason otherwise.
#
# The path must be an ABSOLUTE .jsonl (task.cue's _observation regex already pinned
# that; this refuses the two mis-filings anyway, so a bypassed schema is still
# caught): a relative path or a non-.jsonl is an artifact wearing the wrong list.
observation_resolve() {
  citation="$1"
  root="$2"
  path="$(citation_path "$citation")"
  ranges="$(citation_ranges "$citation")"

  case "$path" in
    /*) : ;;
    *)  printf 'the observation %s is not an absolute transcript path — an observation cites the session .jsonl by absolute path (a repo-relative path is an artifact)\n' "$citation"
        return 1 ;;
  esac
  case "$path" in
    *.jsonl) : ;;
    *)       printf 'the observation %s does not point at a .jsonl transcript — proof the work happened is cited into the session record. A produced file is an artifact.\n' "$citation"
             return 1 ;;
  esac

  if [ ! -f "$path" ] || [ ! -r "$path" ]; then
    printf 'the observation names a transcript that is not there: %s\n' "$path"
    return 1
  fi

  lines="$(citation_lines "$ranges")"
  if [ -z "$lines" ]; then
    printf 'the observation %s names no lines (a reversed range, e.g. 60-40?)\n' "$citation"
    return 1
  fi

  # EVERY cited line must be a tool_result. tool-result exits 0 when the line is one,
  # 1 when it is not (a user message, an assistant turn, no entry), 3 in a sub-agent.
  # A line that is not a tool_result is the agent citing its own prose as proof —
  # exactly the substitution this refuses.
  for n in $lines; do
    sr-session trajectory tool-result --path "$path" --line "$n" >/dev/null 2>&1
    rc=$?
    case "$rc" in
      0) : ;;
      1) printf 'the observation %s cites line %s, which is NOT a tool_result — it is a user message, the agent'\''s own turn, or not an entry. Cite the line whose tool output IS the proof.\n' "$citation" "$n"
         return 1 ;;
      3) printf 'the observation %s is in a sub-agent'\''s trajectory, whose records are the parent'\''s dispatch, not the end user'\''s session.\n' "$citation"
         return 1 ;;
      *) printf 'could not classify line %s of %s (tool-result exited %s).\n' "$n" "$path" "$rc"
         return 1 ;;
    esac
  done
  return 0
}

# --------------------------------------------------- ARTIFACT resolution ------
#
# An artifact is `<repo-relative-file>:<ranges>` into the working TREE. It resolves
# against the FILESYSTEM under the repo root — NEVER through cite/tool-result, which
# read the transcript. The path exists and every cited line exists. Ported from the
# old citations.sh cite_check, with its two measured bug-fixes carried in
# citation_lines above.

# artifact_resolve "<artifact>" "<root>"  ->  0 if the repo-relative citation
# resolves against the tree (file exists under $root, every cited line exists),
# non-zero with a reason otherwise.
#
# It refuses the two shapes that mean the citation was mis-filed:
#   - an ABSOLUTE path — that is an observation's transcript path, and an absolute
#     artifact would not be reviewable from a different checkout;
#   - a .jsonl path — a transcript is proof-the-work-happened (an observation),
#     never a produced result.
# Both are named as such so the agent moves the citation to the right list rather
# than fixing a path. The remaining failures — wrong path, reversed range, range
# past the end — are the file-citation checker's, kept apart because the fixes differ.
artifact_resolve() {
  citation="$1"
  root="$2"
  path="$(citation_path "$citation")"
  ranges="$(citation_ranges "$citation")"

  case "$path" in
    /*) printf 'the artifact %s is an ABSOLUTE path — an artifact is a repo-relative tree file (an absolute .jsonl is an observation)\n' "$citation"
        return 1 ;;
  esac
  case "$path" in
    *.jsonl) printf 'the artifact %s points at a .jsonl transcript — that is an observation (proof it happened), not a produced result. Move it to observations.\n' "$citation"
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
