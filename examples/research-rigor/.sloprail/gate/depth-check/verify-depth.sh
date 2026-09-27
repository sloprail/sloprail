#!/usr/bin/env bash
# Depth check: this research run cloned a real repository AND read its source —
# at least MIN_SOURCE_FILES distinct files (or searched directories) inside a
# directory THIS run cloned, beyond its README and docs. See the example's
# README.md for why depth means reading what was cloned, not counting searches.
#
# Shared: the depth-check Stop gate runs it as its check, and the
# findings-need-depth gate runs it before a research-notes write with
# DEPTH_FOR_WRITE=<path>, so both judge depth by the same rule and word the
# remedy the same way.
set -uo pipefail

# Two distinct source reads. One file can be an entry point that only
# re-exports; a second means the reading followed the implementation past it.
# A floor that separates "opened the repo" from "read how it works", not a
# measure of quality — and meeting it costs the agent real reading either way.
MIN_SOURCE_FILES=2

here="$(cd "$(dirname "$0")" && pwd)"
input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath // empty')"

block() {
  echo "$1" >&2
  exit 1
}

[ -n "$transcript_path" ] || block "The depth check got no transcript path, so what this research run did cannot be read."

# Every trajectory of this research run: this one and each sub-agent it
# dispatched (`describe` lists them) — research handed to a sub-agent is still
# this run's research, and a clone in one and reads in another are one run.
facts="[]"
while IFS= read -r traj; do
  [ -f "$traj" ] || continue
  one="$(sr-session trajectory normalize --path "$traj" --events PreCommandInvoke 2>/dev/null \
    | jq -c --arg ws "${SR_WORKSPACE:-}" --arg home "${HOME:-}" -f "$here/research-facts.jq" 2>/dev/null)"
  [ -n "${one:-}" ] || continue
  facts="$(printf '%s' "$facts" | jq -c --argjson f "$one" '. + [$f]')"
done <<EOF
$transcript_path
$(sr-session trajectory describe --path "$transcript_path" 2>/dev/null | jq -r '.subagentPaths[]?' 2>/dev/null)
EOF

# The verdict over the whole run: which clone directories count, which reads
# landed inside them, and what the agent read elsewhere (for the refusal).
verdict="$(printf '%s' "$facts" | jq -c --argjson min "$MIN_SOURCE_FILES" \
  --arg ws "${SR_WORKSPACE:-}" --arg home "${HOME:-}" '
  def canon: sub("^/private(?<rest>/(tmp|var|etc)(/.*)?)$"; "\(.rest)");
  def under($d): . == $d or startswith($d + "/");
  # A README, changelog, licence, or anything under a docs directory says what
  # a library claims, not how it does it.
  def is_doc:
    (split("/") | map(ascii_downcase)) as $parts
    | ($parts | last) as $base
    | ($parts | any(IN("docs", "doc", "documentation", ".git", ".github")))
      or ($base | test("^(readme|changelog|changes|history|license|licence|copying|notice|contributing|code_of_conduct|security|authors|maintainers|codeowners)([.-].*)?$"))
      or ($base | test("\\.(md|markdown|mdx|rst|txt|adoc|asciidoc|org)$"));
  ($ws | canon) as $ws
  | ([ .[].clones[] ] | unique_by(.dest)) as $clones
  | ([ $clones[].dest ]) as $dirs
  | ([ .[].unresolvedClones ] | add // 0) as $unresolved
  | ([ .[].failedClones[]? ] | unique - $dirs) as $failed
  | ([ .[].reads[] ] | unique) as $reads
  | [ $reads[] | . as $p
      | first($dirs[] | select(. as $d | $p | under($d))) as $d
      | ($p | ltrimstr($d) | ltrimstr("/")) as $rel
      | select($rel == "" or ($rel | is_doc | not))
      | $p ] as $source
  | [ $reads[] | . as $p
      | select(any($dirs[]; . as $d | $p | under($d)) | not)
      | select(($ws == "" or ($p | under($ws) | not)) and ($home == "" or ($p | under($home + "/.claude") | not)))
      | select(is_doc | not) ] as $elsewhere
  | {pass: (($dirs | length) > 0 and ($source | length) >= $min),
     dirs: $dirs, unresolved: $unresolved, source: $source, elsewhere: $elsewhere,
     failed: [ $failed[] | select(. as $f | $elsewhere | any(. == $f or startswith($f + "/"))) ]}
')" || block "The depth check could not evaluate this research run's trajectory ($transcript_path)."

if [ "$(printf '%s' "$verdict" | jq -r '.pass')" != "true" ]; then
  reason="$(printf '%s' "$verdict" | jq -r --argjson min "$MIN_SOURCE_FILES" '
    def list($xs): ($xs[:3] | join(", ")) + (if ($xs | length) > 3 then ", …" else "" end);
    def more($n): if $n == 1 then "1 more distinct source file" else "\($n) more distinct source files" end;
    (if (.dirs | length) == 0 then
       "This #research run has not cloned a repository: no git clone in it (or in a sub-agent it dispatched) succeeded"
       + (if .unresolved > 0 then " into a directory that can be located — clone into a literal path, not one built from a variable or reached through an unresolvable cd" else "" end)
       + ". To finish the research: git clone a real repository that implements what you are researching, then read at least \($min) of its source files (not only the README or docs) with Read, Grep, cat, sed, grep or rg."
     else
       (if (.dirs | length) == 1 then "its" else "their" end) as $its
       | "This #research run cloned " + list(.dirs) + " but read "
       + (if (.source | length) == 0 then "none of " + $its + " source files"
          else "only one source file " + (if (.dirs | length) == 1 then "in it" else "across them" end)
               + " (" + list(.source) + "), and \($min) are needed" end)
       + " — a README or docs file does not count. To finish the research: read "
       + more($min - (.source | length)) + " inside " + list(.dirs)
       + " with Read, Grep, cat, sed, grep or rg; reading the same file again does not add one."
     end)
    + (if (.failed | length) > 0 then
         " Your git clone into " + list(.failed) + " failed because the directory was already there, so its contents are not this run'"'"'s clone — clone into a new directory to use that repository."
       else "" end)
    + (if (.elsewhere | length) > 0 then
         " Reads of directories this run did not clone do not count (e.g. " + list(.elsewhere) + ") — a checkout already on disk is not research this run did."
       else " Reads of directories this run did not clone do not count." end)
  ')"
  # Invoked by findings-need-depth before a research-notes write: say why the
  # write is held, so the agent reads first and writes after.
  if [ -n "${DEPTH_FOR_WRITE:-}" ]; then
    reason="Writing $DEPTH_FOR_WRITE now would record this #research run's findings before the research has depth — do the reading first, then write it. $reason"
  fi
  block "$reason"
fi

# A write is judged on depth alone; the trajectory-shape check below is about
# how the research ran, which the Stop gate answers.
[ -z "${DEPTH_FOR_WRITE:-}" ] || exit 0

# Each research trajectory must be its own agent. Only meaningful inside a
# subagent run: refuse when this ran as a subagent (.isSubagent) that carries
# sibling trajectory paths (.subagentPaths) alongside it.
if ! described="$(sr-session trajectory describe --path "$transcript_path" 2>&1)"; then
  block "Could not describe this research run's trajectory ($transcript_path), so whether it ran as its own agent is unknown: $described"
fi
# Checked by VALUE, not jq's exit status: some jq builds exit 0 on unparseable
# or empty input, which would read as "not a subagent" and permit.
is_subagent="$(printf '%s' "$described" | jq -r '.isSubagent' 2>/dev/null)"
case "$is_subagent" in
  true | false) ;;
  *) block "trajectory describe did not report isSubagent for $transcript_path, so whether this research ran as its own agent is unknown." ;;
esac

if [ "$is_subagent" = "true" ]; then
  sibling_count="$(printf '%s' "$described" | jq '(.subagentPaths // []) | length' 2>/dev/null)"
  case "$sibling_count" in
    '' | *[!0-9]*) block "trajectory describe returned unreadable subagentPaths for $transcript_path, so sibling trajectories could not be counted." ;;
  esac

  if [ "$sibling_count" -gt 0 ]; then
    block "This research ran in a subagent trajectory alongside ${sibling_count} sibling trajectories — each research trajectory must run as its own separate agent, not share one with others."
  fi
fi

exit 0
