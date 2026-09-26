#!/usr/bin/env bash
# The citation half this example reuses from sloprail-tasks's
# task-body-is-human-authored guard (see
# marketplace/plugins/sloprail-tasks/.sloprail/file-guard/task-evidence-resolves/cite-links.sh)
# — the SAME `[quote](jsonl-path)` grammar and the SAME `sr-session trajectory
# cite` grounding call, trimmed to just the two functions this gate needs. The
# body-citation guard grounds a TASK's ask; this grounds a GOAL's ask. Copied
# rather than sourced across plugin/example boundaries — an example ships
# standalone and must not depend on a marketplace plugin's internal layout.

# ask_citation_extract "<text>"  ->  the first `<jsonl-path>\t<quote>` found in
# a `[quote](jsonl-path[:line])` link, or nothing if there is none.
ask_citation_extract() {
  printf '%s' "$1" | perl -ne '
    if (/\[([^\]]+)\]\(([^)\s]+\.jsonl(?::[0-9]+)?)\)/) {
      my ($quote, $href) = ($1, $2);
      print "$href\t$quote\n";
      exit;
    }
  ' 2>/dev/null
}

# ask_citation_href_path "<href>"  ->  the .jsonl path with any `:line` suffix
# stripped (cite resolves the line itself).
ask_citation_href_path() {
  case "$1" in
    *.jsonl:[0-9]*) printf '%s' "${1%:*}" ;;
    *)              printf '%s' "$1" ;;
  esac
}

# ask_citation_ground "<jsonl-path>" "<quote>"  ->  0 if cite resolves the
# quote to the USER pool of that trajectory, non-zero with a reason otherwise.
# Mirrors cite_ground's user-pool branch in the sloprail-tasks library.
ask_citation_ground() {
  cpath="$1"
  quote="$2"

  if [ ! -f "$cpath" ]; then
    printf 'the cited_ask names a transcript that is not there: %s\n' "$cpath"
    return 1
  fi

  sr-session trajectory cite --source-types user --path "$cpath" "$quote" >/dev/null 2>&1
  rc=$?
  case "$rc" in
    0) return 0 ;;
    1) printf 'the cited_ask quote "%s" resolves to nothing in the user pool of %s — it is not the user'\''s own words. Quote the exact text they wrote.\n' "$quote" "$cpath"
       return 1 ;;
    2) printf 'the cited_ask quote "%s" matches SEVERAL user messages in %s — extend it until it lands on exactly one.\n' "$quote" "$cpath"
       return 1 ;;
    3) printf 'cite cannot ground "%s" here: this trajectory is a sub-agent'\''s, not the end user'\''s session.\n' "$quote"
       return 1 ;;
    *) printf 'could not verify the cited_ask quote "%s" against %s (cite exited %s).\n' "$quote" "$cpath" "$rc"
       return 1 ;;
  esac
}
