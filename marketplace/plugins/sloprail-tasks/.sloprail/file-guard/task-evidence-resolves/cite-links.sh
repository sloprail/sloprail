# Citation LINKS, sourced by every guardrail that grounds a claim in the user's
# own words. This is the ONE place the task guardrails understand a citation, and
# it deliberately owns almost no logic: the grammar of a link, and a call to
# `sr-session trajectory cite` for the actual grounding. cite is the single
# validator (2026-08-21, coordinator): it resolves a quote to the user's own line
# in a trajectory and — crucially — REFUSES a quote that is really a
# <system-reminder> / <task-notification> / <local-command> (a harness-injected
# user-role message, not the person's typing) or an ordinary tool result. So this
# file walks NO transcript and filters NO tags; it hands cite the quote and the
# path and trusts cite's verdict. That is why there is no citations.sh here any
# more — the transcript-walking and the tag-exclusion moved into cite, where every
# consumer gets them for free.
#
# THE CITATION FORMAT is a MARKDOWN LINK: `[<quote>](<jsonl-path>)`.
#   - the link TEXT is the quote — the user's own words, verbatim.
#   - the HREF is the transcript .jsonl path the quote should resolve in,
#     optionally with a `:line` suffix (`/abs/session.jsonl` or `.jsonl:120`).
# A TASK.md carries ONLY such links as its grounding evidence; anything hand-
# written that is not a resolvable citation is not evidence.
#
# Nothing here exits. These are functions that print and return, so the calling
# guardrail decides what a failure means. This file reads no event and dispatches
# on no kind — it is a pure library, so the flat-event / resultKnown discipline the
# check scripts follow does not apply to it.

# cite_links_extract "<markdown text>"  ->  one `<jsonl-path>\t<quote>` per line,
# for every `[quote](jsonl-path)` link in the text.
#
# TAB-separated because a path cannot contain a tab and a quote is unlikely to; it
# lets a caller split cleanly with `cut`. The quote is the link text as written
# (the user's words); the path is the href, `.jsonl` optionally `:line`. A link
# whose href is not a .jsonl is skipped here — evidence grounds in the transcript,
# and a non-.jsonl href is not a citation this rule understands.
#
# perl for the parse: the link text may contain punctuation a naive grep tears on,
# and perl's non-greedy capture handles several links on one line. perl is present
# on macOS and Linux. Falls back to nothing (no links) if perl is absent, which the
# caller reads as "no citation" and refuses — the safe direction.
cite_links_extract() {
  printf '%s' "$1" | perl -ne '
    while (/\[([^\]]+)\]\(([^)\s]+\.jsonl(?::[0-9]+)?)\)/g) {
      my ($quote, $href) = ($1, $2);
      print "$href\t$quote\n";
    }
  ' 2>/dev/null
}

# cite_link_href_path "<href>"  ->  the .jsonl path with any `:line` suffix
# stripped, so cite is handed the file path it wants (cite resolves the line
# itself). `%%:*`-style trim, but only when the suffix is a bare line number, so a
# path that somehow contained a colon is left alone.
cite_link_href_path() {
  case "$1" in
    *.jsonl:[0-9]*) printf '%s' "${1%:*}" ;;
    *)              printf '%s' "$1" ;;
  esac
}

# cite_ground "<jsonl-path>" "<quote>"  ->  returns 0 if cite resolves the quote to
# the user's own words in that trajectory, non-zero otherwise, printing a reason on
# the non-zero paths. cite is the authority; this only maps its exit codes to a
# fix-naming reason, because each code is a DIFFERENT fix for the agent.
#
#   0  exactly one match — grounded. Silent, returns 0.
#   1  no match          — the quote is not the user's words (a paraphrase, a
#                          fabrication, or a harness-injected message cite excludes).
#   2  several matches    — ambiguous; extend the quote until it lands on one.
#   3  sub-agent          — cite cannot ground here (the "user" messages are the
#                          parent's dispatch prompt); reported, not silently passed.
#   *  other              — cite could not run.
#
# cite MUST be given --path explicitly: with no --path it fails closed (it cannot
# rule out a sub-agent from a tool call's environment), so a bare `cite "$quote"`
# would refuse every citation. The path comes from the link's own href.
cite_ground() {
  cpath="$1"
  quote="$2"

  if [ ! -f "$cpath" ]; then
    printf 'the citation names a transcript that is not there: %s\n' "$cpath"
    return 1
  fi

  sr-session trajectory cite --path "$cpath" "$quote" >/dev/null 2>&1
  rc=$?
  case "$rc" in
    0) return 0 ;;
    1) printf 'the quote "%s" resolves to nothing the user said in %s — a paraphrase, a fabrication, or a harness-injected message. Quote the user'\''s own words verbatim.\n' "$quote" "$cpath"
       return 1 ;;
    2) printf 'the quote "%s" matches SEVERAL user messages in %s — extend it until it lands on exactly one.\n' "$quote" "$cpath"
       return 1 ;;
    3) printf 'cite cannot ground "%s" here: inside a sub-agent the "user" messages are the parent'\''s dispatch prompt, not the end user'\''s words.\n' "$quote"
       return 1 ;;
    *) printf 'could not verify the quote "%s" against %s (cite exited %s).\n' "$quote" "$cpath" "$rc"
       return 1 ;;
  esac
}
