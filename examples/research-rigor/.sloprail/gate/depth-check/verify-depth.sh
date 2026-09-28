#!/usr/bin/env bash
# Depth check: this research run cloned a real repository AND read its source —
# at least MIN_SOURCE_FILES distinct files (or searches of a source
# subdirectory that printed something) inside a directory THIS run cloned —
# confirmed by git's own output or on disk — beyond its README and docs. See
# the example's README.md for why depth means reading what was cloned, not
# counting searches.
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

# A trajectory that cannot be read is not evidence of anything the agent did or
# did not do: the refusal says it could not be read, rather than "has not
# cloned", which would send the agent to clone again for a failure not its own.
unreadable() {
  block "The depth check could not read this #research run's trajectory $1, so whether the research has depth is unknown: $2"
}

# A tool's stderr is kept apart from the JSON on its stdout — a warning on
# stderr must not corrupt what is parsed — and shown only when the tool fails.
errf="$(mktemp)" || block "The depth check could not create a temporary file, so this research run was not checked."
trap 'rm -f "$errf"' EXIT
errtext() { tr '\n' ' ' < "$errf" | cut -c1-400; }

# Every trajectory of this research run: this one and each sub-agent it
# dispatched (`describe` lists them) — research handed to a sub-agent is still
# this run's research, and a clone in one and reads in another are one run.
if ! described_run="$(sr-session trajectory describe --path "$transcript_path" 2>"$errf")"; then
  unreadable "$transcript_path" "$(errtext)"
fi
if ! subagents="$(printf '%s' "$described_run" | jq -r '.subagentPaths[]?' 2>"$errf")"; then
  unreadable "$transcript_path" "trajectory describe returned no readable subagentPaths: $(errtext)"
fi
facts="[]"
while IFS= read -r traj; do
  [ -n "$traj" ] || continue
  [ -f "$traj" ] || unreadable "$traj" "the file does not exist"
  if ! entries="$(sr-session trajectory normalize --path "$traj" --events PreCommandInvoke 2>"$errf")"; then
    unreadable "$traj" "$(errtext)"
  fi
  if ! one="$(printf '%s' "$entries" | jq -c -L "$here" --arg ws "${SR_WORKSPACE:-}" --arg home "${HOME:-}" -f "$here/research-facts.jq" 2>"$errf")" \
    || [ -z "$one" ]; then
    unreadable "$traj" "its research facts could not be computed: $(errtext)"
  fi
  facts="$(printf '%s' "$facts" | jq -c --argjson f "$one" '. + [$f]')"
done <<EOF
$transcript_path
$subagents
EOF

# Whether a clone happened is read from git's own record of it, not from the
# command's output (which the agent controls: `2>/dev/null`, `|| echo`, `echo
# "Cloning into …"`): the first line of <dest>/.git/logs/HEAD, which git
# writes as `<old> <new> <who> <epoch> <tz>\tclone: from <url>`. The clone
# counts when that names the repository the invocation cloned and is no older
# than this session's first record — a checkout from an earlier session, or a
# .git copied or moved from one, carries its original line; a hand-made .git
# carries none. Nothing here reads file times, so it holds on any filesystem.
since="$(jq -rn 'first(inputs | .timestamp? | strings) | sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601' < "$transcript_path" 2>/dev/null)"
reflogs="[]"
while IFS= read -r dest; do
  [ -n "$dest" ] || continue
  gitdir="$dest/.git"
  if [ -f "$gitdir" ]; then
    # --separate-git-dir: .git is a file naming the real one.
    gd="$(sed -n 's/^gitdir: //p' "$gitdir" | head -n 1)"
    case "$gd" in /*) gitdir="$gd" ;; ?*) gitdir="$dest/$gd" ;; esac
  fi
  first="$(head -n 1 "$gitdir/logs/HEAD" 2>/dev/null)"
  # Beyond the line itself: the commit it says the clone checked out exists in
  # the repository, and one of its remotes is what it was cloned from — a
  # hand-written reflog over hand-made files has neither.
  newsha="$(printf '%s' "$first" | cut -f1 | awk '{print $2}')"
  hascommit=false
  [ -n "$newsha" ] && git --git-dir="$gitdir" cat-file -e "$newsha^{commit}" 2>/dev/null && hascommit=true
  # Every remote's URL: `git clone -o upstream` names its remote otherwise.
  remotes="$(git --git-dir="$gitdir" config --get-regexp '^remote\..*\.url$' 2>/dev/null | cut -d' ' -f2- | jq -R -s -c 'split("\n") | map(select(. != ""))')"
  reflogs="$(printf '%s' "$reflogs" | jq -c --arg d "$dest" --arg l "$first" --argjson c "$hascommit" --argjson r "${remotes:-[]}" \
    '. + [{dest: $d, line: $l, commit: $c, remotes: $r}]')"
done <<EOF
$(printf '%s' "$facts" | jq -r '[ .[].clones[].dest ] | unique[]')
EOF

# What each read path really is: its target once symlinks are resolved, and
# its file identity (device:inode). `lib2 -> lib` is not a second directory,
# a hard link is not a second file, and a symlink out of a clone is not a read
# inside it.
if stat --version >/dev/null 2>&1; then
  ident() { stat -L -c %d:%i -- "$1" 2>/dev/null; }
else
  ident() { stat -L -f %d:%i -- "$1" 2>/dev/null; }
fi
real() { realpath -q -- "$1" 2>/dev/null || readlink -f -- "$1" 2>/dev/null || printf '%s' "$1"; }
paths="$(printf '%s' "$facts" | jq -r '[ .[].reads[], .[].clones[].dest ] | unique[]' | while IFS= read -r p; do
  [ -n "$p" ] || continue
  printf '%s\t%s\t%s\n' "$p" "$(real "$p")" "$(ident "$p")"
done | jq -R -s -c 'split("\n") | map(select(. != "") | split("\t") | {key: .[0], value: {real: .[1], id: .[2]}}) | from_entries')"
[ -n "$paths" ] || paths="{}"

# A Read with a small limit whose shown lines ran to the file's end showed the
# whole file (a short file read in one go): it counts as a full read.
endreads="$(printf '%s' "$facts" | jq -r '.[].partialEnds[]? | "\(.last)\t\(.path)"' | while IFS="$(printf '\t')" read -r last p; do
  [ -f "$p" ] || continue
  n="$(wc -l < "$p" 2>/dev/null | tr -d ' ')"
  [ -n "$n" ] && [ "$n" -le "$last" ] 2>/dev/null && printf '%s\n' "$p"
done | jq -R -s -c 'split("\n") | map(select(. != ""))')"
[ -n "$endreads" ] || endreads="[]"

# The verdict over the whole run: which clone directories count, which reads
# landed inside them, and what the agent read elsewhere (for the refusal).
verdict="$(printf '%s' "$facts" | jq -c -L "$here" --argjson min "$MIN_SOURCE_FILES" \
  --argjson reflogs "$reflogs" --argjson paths "$paths" --arg since "${since:-}" --argjson endreads "$endreads" \
  --arg ws "${SR_WORKSPACE:-}" --arg home "${HOME:-}" '
  include "paths";
  def under($d): . == $d or startswith($d + "/");
  # A README, changelog, licence, anything under a docs directory, and the
  # project metadata around the code (manifests, lockfiles, dotfiles, CI and
  # build config) say what a library claims or how it is built, not how it
  # works.
  def is_doc:
    (split("/") | map(ascii_downcase)) as $parts
    | ($parts | last) as $base
    | ($parts | any(IN("docs", "doc", "documentation", ".git", ".github", ".circleci", ".gitlab", ".vscode", ".idea")))
      or ($base | test("^(readme|changelog|changes|history|license|licence|copying|notice|contributing|code_of_conduct|security|authors|maintainers|codeowners)([.-].*)?$"))
      or ($base | test("\\.(md|markdown|mdx|rst|txt|adoc|asciidoc|org|lock)$"))
      or ($base | startswith("."))
      or ($base | test("^(package(-lock)?\\.json|npm-shrinkwrap\\.json|yarn\\.lock|pnpm-lock\\.yaml|bun\\.lockb|go\\.(mod|sum|work)|cargo\\.(toml|lock)|pyproject\\.toml|setup\\.cfg|pipfile(\\.lock)?|poetry\\.lock|requirements[^/]*\\.(txt|in)|gemfile(\\.lock)?|[^/]*\\.gemspec|composer\\.(json|lock)|pom\\.xml|(build|settings)\\.gradle(\\.kts)?|gradle\\.properties|tsconfig[^/]*\\.json|jsconfig\\.json|tox\\.ini|renovate\\.json|dependabot\\.ya?ml|codecov\\.ya?ml|appveyor\\.ya?ml|azure-pipelines\\.ya?ml|mkdocs\\.ya?ml)$"));
  # Where a path really is (symlinks resolved) and which file it is.
  def realof: . as $p | ($paths[$p].real // "" | if . == "" then $p else . end | canon);
  def idof: . as $p | ($paths[$p].id // "" | if . == "" then ($p | realof) else . end);
  ($since | if . == "" then null else tonumber end) as $since
  | ($ws | canon) as $ws
  # A clone counts when the record git wrote says it was cloned from that
  # repository during this session.
  | ([ $reflogs[]
      | .dest as $d
      | (.line | split("\t")) as $parts
      | select(($parts | length) >= 2)
      | ($parts[0] | split(" ") | .[-2] | tonumber? // null) as $ts
      | ($parts[1:] | join("\t") | capture("^clone: from (?<url>.*)$")? | .url | repokey(null)) as $url
      | select($ts != null and $since != null and $ts >= ($since | floor))
      | select(.commit == true and any(.remotes[]; repokey(null) == $url))
      | {key: $d, value: $url} ] | from_entries) as $cloned
  # A clone of this project itself is not prior art: it reads what the agent
  # is meant to be researching FOR.
  | ([ .[].clones[] | select(.repo != null and $ws != "" and (.repo == $ws or (.repo | startswith($ws + "/")))) | .dest ] | unique) as $self
  | ([ .[].clones[] | select(.repo != null and $cloned[.dest] == .repo) | .dest ] | unique - $self) as $dirs
  | ([ .[].clones[].dest ] | unique - $dirs - $self) as $unconfirmed
  | ([ .[].unresolvedClones ] | add // 0) as $unresolved
  | ([ .[].failedClones[]? ] | unique - $dirs) as $failed
  | ([ .[].reads[] ] | unique) as $reads
  | ([ .[].fullReads[]?, $endreads[] ] | unique) as $fullreads
  | [ $dirs[] | realof ] as $realdirs
  # Source: really under a confirmed clone, below its root (a search of the
  # root takes in the README and docs too), not documentation or metadata —
  # one per file, however many spellings reached it.
  | ([ $reads[] | . as $p | ($p | realof) as $rp
      | first($realdirs[] | select(. as $d | $rp | under($d))) as $d
      | ($rp | ltrimstr($d) | ltrimstr("/")) as $rel
      | select($rel != "" and ($rel | is_doc | not))
      | {p: $p, id: ($p | idof)} ]
    | unique_by(.id) | map(.p)) as $source
  # Credited source files every read of which showed only part (head -c 1,
  # sed -n 1p, a Read with limit): credited, and reported for the eval.
  | [ $source[] | select(. as $p | $fullreads | index($p) | not) ] as $glimpsed
  | [ $reads[] | . as $p
      | select(any($realdirs[]; . as $d | $p | realof | under($d)) | not)
      | select(($ws == "" or ($p | under($ws) | not)) and ($home == "" or ($p | under($home + "/.claude") | not)))
      | select(is_doc | not) ] as $elsewhere
  | {pass: (($dirs | length) > 0 and ($source | length) >= $min),
     dirs: $dirs, unresolved: $unresolved, source: $source, elsewhere: $elsewhere,
     failed: [ $failed[] | select(. as $f | $elsewhere | any(. == $f or startswith($f + "/"))) ],
     unconfirmed: $unconfirmed, self: $self, glimpsed: $glimpsed}
' 2>"$errf")" || block "The depth check could not evaluate this research run's trajectory ($transcript_path): $(errtext)"

# DEPTH_REPORT=<file>: the verdict itself, for a caller that needs more than
# pass/fail (the eval's scorer asks which credited reads were glimpses).
[ -z "${DEPTH_REPORT:-}" ] || printf '%s' "$verdict" > "$DEPTH_REPORT"

if [ "$(printf '%s' "$verdict" | jq -r '.pass')" != "true" ]; then
  # An undeclared run is held for its proposal alone: "#research run" would
  # name a declaration it never made.
  label="This #research run"
  if [ -n "${DEPTH_PROPOSAL:-}" ] || [ "$(printf '%s' "$input" | jq -r '.context["research-run"].payload.declared == false')" = "true" ]; then
    label="This run"
  fi
  reason="$(printf '%s' "$verdict" | jq -r --argjson min "$MIN_SOURCE_FILES" --arg label "$label" '
    def list($xs): ($xs[:3] | join(", ")) + (if ($xs | length) > 3 then ", …" else "" end);
    def more($n): if $n == 1 then "1 more distinct source file" else "\($n) more distinct source files" end;
    (if (.dirs | length) == 0 then
       $label + " has not cloned a repository: no git clone in it (or in a sub-agent it dispatched) succeeded"
       + (if .unresolved > 0 then " into a directory that can be located — clone into a literal path, not one built from a variable or reached through an unresolvable cd" else "" end)
       + ". To finish the research: git clone a real repository that implements what you are researching, then read at least \($min) of its source files (not only the README or docs) with Read, Grep, cat, sed, grep or rg."
     else
       (if (.dirs | length) == 1 then "its" else "their" end) as $its
       | $label + " cloned " + list(.dirs) + " but read "
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
    + (if (.unconfirmed | length) > 0 then
         " Your git clone into " + list(.unconfirmed) + " could not be confirmed: git'"'"'s own record of the clone (.git/logs/HEAD) is missing, older than this session, or names a different repository, so it may be a checkout that was already on disk — clone into a new directory."
       else "" end)
    + (if (.self | length) > 0 then
         " Your clone into " + list(.self) + " is of this project itself, which is not prior art — clone a real repository that implements what you are researching."
       else "" end)
    + (if (.elsewhere | length) > 0 then
         " Reads of directories this run did not clone do not count (e.g. " + list(.elsewhere) + ") — a checkout already on disk is not research this run did."
       else " Reads of directories this run did not clone do not count." end)
  ')"
  # Invoked by findings-need-depth before a research-notes write: say why the
  # write is held, so the agent reads first and writes after.
  if [ -n "${DEPTH_FOR_WRITE:-}" ] && [ -n "${DEPTH_PROPOSAL:-}" ]; then
    reason="Writing $DEPTH_FOR_WRITE now would add a Proposed approach before any research — this project's NOTES.md requires researching real prior art before proposing, whether or not #research was declared. Do the reading first, then write it. $reason"
  elif [ -n "${DEPTH_FOR_WRITE:-}" ]; then
    reason="Writing $DEPTH_FOR_WRITE now would record this #research run's findings before the research has depth — do the reading first, then write it. $reason"
  elif [ "$(printf '%s' "$input" | jq -r '.context["research-run"].payload.proposal // empty')" != "" ]; then
    reason="$(printf '%s' "$input" | jq -r '.context["research-run"].payload.proposal') now holds a Proposed approach, and this project's NOTES.md requires researching real prior art before proposing — whether or not #research was declared. $reason"
  fi
  block "$reason"
fi

# A write is judged on depth alone; the trajectory-shape check below is about
# how the research ran, which the Stop gate answers.
[ -z "${DEPTH_FOR_WRITE:-}" ] || exit 0

# Each research trajectory must be its own agent. Only meaningful inside a
# subagent run: refuse when this ran as a subagent (.isSubagent) that carries
# sibling trajectory paths (.subagentPaths) alongside it.
described="$described_run"
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
