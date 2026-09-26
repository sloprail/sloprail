#!/usr/bin/env bash
# compute-draft-facts.sh <content>  ->  a JSON object of cheap, deterministic
# facts about a unit's shipped copy, printed on stdout.
#
# WHY THIS EXISTS: a real run showed the judge spending most of its turns
# measuring things one Bash call at a time (a title's length, the body's
# word count, an em-dash tally) instead of weighing the rules — one tool
# call per turn against the model's own harness, for numbers a shell script
# can produce once, for free, before the judge ever starts. This script
# computes them here and prepare-judge-rules.sh hands them to the judge as
# additionalContext.facts; judge-rules.md.j2 tells the judge to use these
# numbers and reach for its own Bash measurement only when a rule needs a
# fact not covered here (a banned-phrase grep, a rule-specific delimiter).
#
# WHAT'S COMPUTED, and the definitions a rule's own text can rely on:
#   title_line        the content's first non-empty line (frontmatter, the
#                      leading "---\n...\n---\n" block, is stripped first)
#   title_chars        that line's character count (wc -m, not bytes)
#   body_word_count    words in the body AFTER frontmatter and the title
#                      line are excluded — the prose that actually ships
#   em_dash_count      occurrences of U+2014 (—) anywhere in the raw content
#   emoji_count        codepoints in the common emoji ranges anywhere in the
#                      raw content (a heuristic count, not a Unicode-property
#                      grep — see the awk script below for exactly which
#                      ranges)
#   segments           for a thread split on a line containing exactly
#                      `---` (the plugin README's own example delimiter):
#                      each segment's OWN character count, in order, as an
#                      array of integers. A unit with no `---` line has
#                      exactly one segment: the whole body.
#
# All counts use `wc -m` (characters), never `wc -c` (bytes), matching the
# rule text this plugin's own README shows ("Measure each segment's
# character count with `wc -m`").
#
# Emits `{}` (still exit 0) on empty input — nothing to measure is not a
# failure.
set -uo pipefail

content="${1-}"

if [ -z "$content" ]; then
  echo '{}'
  exit 0
fi

# Strip a single leading frontmatter block ("---\n...\n---\n") if present,
# to get the BODY the title/word-count facts are defined against.
body_no_fm="$(printf '%s' "$content" | awk '
  BEGIN { in_fm = 0; started = 0; done = 0 }
  {
    if (!started) {
      started = 1
      if ($0 == "---") { in_fm = 1; next }
    } else if (in_fm && !done) {
      if ($0 == "---") { in_fm = 0; done = 1; next }
      next
    }
    print
  }
')"

# title_line: the first non-empty line of the frontmatter-stripped body.
title_line="$(printf '%s\n' "$body_no_fm" | awk 'NF { print; exit }')"
title_chars=0
if [ -n "$title_line" ]; then
  title_chars="$(printf '%s' "$title_line" | wc -m | tr -d ' ')"
fi

# body_after_title: the frontmatter-stripped body with the title line's
# first occurrence removed, for the word count a rule means by "the body".
body_after_title="$(printf '%s\n' "$body_no_fm" | awk -v t="$title_line" '
  BEGIN { removed = 0 }
  { if (!removed && NF && $0 == t) { removed = 1; next } print }
')"
body_word_count="$(printf '%s' "$body_after_title" | wc -w | tr -d ' ')"

# em_dash_count: occurrences of U+2014 anywhere in the raw content.
em_dash_count="$(printf '%s' "$content" | grep -o "—" 2>/dev/null | wc -l | tr -d ' ')"

# emoji_count: a heuristic count of codepoints in the common emoji blocks.
# Done via jq's `explode` (codepoint array) rather than `grep -P`/`\x{...}`
# ranges — BSD grep (macOS's default /usr/bin/grep) has no -P support, so a
# PCRE-only range would silently fail wherever this guard runs outside
# Linux CI. jq is already this plugin's one hard dependency everywhere else,
# so counting codepoints through it keeps this portable. Not exhaustive
# (Unicode's emoji ranges are scattered) but covers the ranges emoji
# actually used in content practically fall in: misc symbols/pictographs,
# emoticons, transport/map symbols, supplemental symbols, and the dingbats
# block.
emoji_count="$(printf '%s' "$content" | jq -Rsr '
  [explode[] | select(
    (. >= 127744 and . <= 129791) or
    (. >= 9728 and . <= 10175)
  )] | length
' 2>/dev/null)"
if [ -z "$emoji_count" ]; then
  emoji_count=0
fi

# segments: split the RAW content on a line containing exactly `---`
# (the thread delimiter the README's own example rule names), each
# segment's own character count via wc -m. A frontmatter block also uses
# bare `---` lines, so the split is over the FRONTMATTER-STRIPPED body, not
# the raw content — otherwise a unit's own frontmatter fence would masquerade
# as a thread delimiter and produce a spurious empty leading segment.
#
# Segments are split into NUL-delimited chunks (a segment's own text may
# contain blank lines, so a plain newline-delimited read is not safe) and
# each is measured with wc -m, in order.
segments_json="$(printf '%s\n' "$body_no_fm" | awk '
  BEGIN { seg = ""; n = 0 }
  $0 == "---" { printf "%s%c", seg, 0; seg = ""; n++; next }
  { seg = seg $0 "\n" }
  END { printf "%s%c", seg, 0 }
' | {
  out="["
  first=1
  while IFS= read -r -d $'\0' seg; do
    c="$(printf '%s' "$seg" | wc -m | tr -d ' ')"
    if [ "$first" -eq 1 ]; then first=0; else out="${out},"; fi
    out="${out}${c}"
  done
  out="${out}]"
  printf '%s' "$out"
})"

jq -n \
  --arg title_line "$title_line" \
  --argjson title_chars "${title_chars:-0}" \
  --argjson body_word_count "${body_word_count:-0}" \
  --argjson em_dash_count "${em_dash_count:-0}" \
  --argjson emoji_count "${emoji_count:-0}" \
  --argjson segments "${segments_json:-[]}" \
  '{
    title_line: $title_line,
    title_chars: $title_chars,
    body_word_count: $body_word_count,
    em_dash_count: $em_dash_count,
    emoji_count: $emoji_count,
    segment_char_counts: $segments
  }'
