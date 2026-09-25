#!/usr/bin/env bash
# A shipped SCRIPT rule: the unit's body must not match any pattern in a
# project-supplied list — the deterministic half of "no AI-sounding text": a
# specific banned word/phrase, or a regex for something structural an LLM is bad
# at self-policing (em-dash overuse, a repeated stock opener). The list itself is
# NOT shipped — this plugin ships the mechanism; the project supplies the words,
# because what reads as an AI tell is house style, not a universal.
#
# Args (positional, from the rule's `script.args`):
#   $1  list-path   a project-relative path (resolved under $SR_WORKSPACE) to a
#                    plain text file, ONE PATTERN PER LINE. A line is a literal
#                    substring by default; a line wrapped in `/.../ ` is a
#                    perl-compatible REGEX (so "em-dash used 3+ times" or
#                    "\bdelve\b" style patterns are expressible, not just literal
#                    words). Blank lines and lines starting with `#` are
#                    comments, skipped.
#
# Reads the SAME CheckPayload every check reads, on stdin — see char-limit.sh's
# header for how unit-satisfies-rules dispatches a script rule.
#
# REFUSAL CONTRACT: exit 0 permits; non-zero refuses with `{"reason": "..."}` on
# stdout, naming every matched pattern (not just the first) so one fix pass
# catches them all.
set -uo pipefail

refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

list_rel="${1:-}"
if [ -z "$list_rel" ]; then
  refuse "banned-phrases: the rule's script.args[0] must name the project-relative banned-phrase list file"
fi

root="${SR_WORKSPACE:-.}"
list="$root/$list_rel"
if [ ! -f "$list" ]; then
  refuse "banned-phrases: the list file '$list_rel' does not exist under $root — a script rule cannot run without the list the project promised in its args"
fi

event="$(cat)"
content="$(printf '%s' "$event" | jq -r '.event.newContent // empty' 2>/dev/null)"
if [ -z "$content" ]; then
  exit 0
fi

body="$(printf '%s' "$content" | perl -0777 -ne '
  if (/\A---\n.*?\n---\n(.*)\z/s) { print $1 } else { print }
')"

# perl does the matching: a literal line is quoted with \Q..\E, a /regex/ line
# is used as-is (case-insensitive, /i, matching the "AI tells" use case — a
# banned word is banned regardless of case). One report line per matched
# pattern, so the refusal names everything at once.
report="$(perl -e '
  my $body = do { local $/; <STDIN> };
  my $list_path = $ARGV[0];
  open(my $fh, "<", $list_path) or die "cannot open $list_path: $!";
  while (my $line = <$fh>) {
    chomp $line;
    next if $line =~ /^\s*$/;
    next if $line =~ /^\s*#/;
    my $pattern = $line;
    my $is_regex = 0;
    if ($pattern =~ m{^/(.*)/$}) {
      $pattern = $1;
      $is_regex = 1;
    }
    my $re = $is_regex ? qr/$pattern/i : qr/\Q$pattern\E/i;
    my @matches = ($body =~ /$re/g);
    if (@matches) {
      my $n = scalar(@matches);
      print "  matches \"$line\" ($n occurrence" . ($n == 1 ? "" : "s") . ")\n";
    }
  }
' "$list" <<<"$body")"

if [ -n "$report" ]; then
  refuse "BANNED PHRASE: the content matches patterns from $list_rel —
$report
Rewrite to avoid these. The list is the project'\''s own; edit $list_rel if a pattern is wrong, not this rule."
fi

exit 0
