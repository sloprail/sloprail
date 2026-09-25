#!/usr/bin/env bash
# A shipped SCRIPT rule: every "segment" of the unit's content is at most N
# characters. Covers the X post/thread limit (280 per tweet) and the HN and
# Reddit title limits (80 / 300) — the task calls for these as SEPARATE reusable
# limits, and this one script serves all three because the only thing that
# differs is the number and how the content is split into segments, both of
# which are arguments, not code.
#
# Args (positional, from the rule's `script.args`):
#   $1  limit           max characters per segment (e.g. 280, 80, 300)
#   $2  delimiter       (optional) the literal string that splits the content
#                        into segments — e.g. a documented thread delimiter like
#                        the literal three characters "---" on its own line.
#                        ABSENT or empty means the WHOLE content is one segment
#                        (a title, a single post).
#
# Reads the SAME CheckPayload every check reads, on stdin — this is invoked as a
# check by unit-satisfies-rules's dispatcher (check-rules.sh), which reads a
# rule's script.name/args out of the rule's own frontmatter and execs the named
# script with those args, piping the SAME event payload through unchanged. So a
# script rule sees `.event.newContent` exactly as unit-satisfies-rules itself
# does — one flat event, no re-derivation.
#
# perl for the split, not awk/sed: RS with a multi-character or newline-bearing
# delimiter is a GNU-awk extension BSD/macOS awk does not implement, and this
# plugin has to run on both. perl's index()-based split has no such divide and
# is already a dependency of this plugin's citation library (cite-links.sh).
#
# REFUSAL CONTRACT: exit 0 permits; non-zero refuses with `{"reason": "..."}` on
# stdout. `set -uo pipefail`, never `set -e` (mirrors every shipped guard script).
set -uo pipefail

refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

limit="${1:-}"
delim="${2:-}"

case "$limit" in
  ''|*[!0-9]*) refuse "char-limit: the rule's script.args[0] must be a positive integer character limit, got '$limit'" ;;
esac

event="$(cat)"
content="$(printf '%s' "$event" | jq -r '.event.newContent // empty' 2>/dev/null)"
if [ -z "$content" ]; then
  # Nothing to check (a delete, or content this check cannot see yet) — permit.
  exit 0
fi

# THE BODY, not the frontmatter: a unit's char-limit rule is about what actually
# ships, and the frontmatter is metadata no channel counts against a post's
# limit. Same frontmatter-stripping extraction every guard in this plugin uses,
# in perl so it composes with the segment split below without a second
# awk/sed dialect to keep portable.
body="$(printf '%s' "$content" | perl -0777 -ne '
  if (/\A---\n.*?\n---\n(.*)\z/s) { print $1 } else { print }
')"

# Split on the literal delimiter (perl index(), not a regex — a delimiter
# containing regex metacharacters, e.g. "---", must split literally). No
# delimiter: the whole body is segment 1.
report="$(perl -e '
  my $body  = do { local $/; <STDIN> };
  my $delim = $ARGV[0];
  my $limit = $ARGV[1] + 0;
  my @segments = length($delim) ? split(/\Q$delim\E/, $body) : ($body);
  my $n = 0;
  for my $seg (@segments) {
    $n++;
    # A segment that is ENTIRELY whitespace (e.g. a split artifact at the very
    # start/end of the body) carries nothing to publish and is not counted.
    next if $seg !~ /\S/;
    my $trimmed = $seg;
    $trimmed =~ s/\A\s+//; $trimmed =~ s/\s+\z//;
    my $len = length($trimmed);
    if ($len > $limit) {
      print "  segment $n is $len characters, over the $limit limit\n";
    }
  }
' "$delim" "$limit" <<<"$body")"

if [ -n "$report" ]; then
  refuse "CHAR LIMIT: over the $limit-character limit —
$report
Shorten it. If this is a thread, each segment between the delimiter is checked separately."
fi

exit 0
