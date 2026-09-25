#!/usr/bin/env bash
# prepare: assemble the JUDGE's rules registry — the primitive-usage rules a
# guardrail script or prompt must get right — from this guard's own judge-rules/
# directory, one rule per judge-rules/<name>/RULE.md. Emit them as an ARRAY under
# additionalContext.rules, which judge.md.j2 iterates with {% for %} to frame the
# prompt. Adding a rule is adding a directory; no prompt is edited.
#
# RULES-AS-ARRAY, not a spliced string. The sibling rule-quality prepare splices
# its meta-rules into a marker in RUBRIC.md, producing one opaque blob. This one
# hands the template a structured array [{name, body}, ...] so the template owns
# the framing (each rule in its own <rule name="..."> element) and a rule's
# name travels beside its body. That is the shape the reviewer asked the judge
# guardrails to use.
#
# The file content being judged is NOT this prepare's job: the judge template
# reads it directly off the flat event (event.newContent / event.path), the same
# field a script check reads from stdin. prepare's ONE job is the rules array.
#
# EXIT 0 with additionalContext on stdout: the rules are assembled; the judge runs
# against them. EXIT 1: a REFUSAL — the check fails closed carrying this script's
# words (a prepare failure fails the check). There is no "permit without judging"
# outcome; a judge fed no standard would be the inert-but-official-looking check
# this whole guard exists to prevent, so an empty registry REFUSES (below), the
# same deliberate asymmetry rule-quality keeps.

set -uo pipefail

# The CheckPayload the engine hands prepare on stdin: the file's own facts are
# FLAT under `.event` (`.event.path`, `.event.newContent`), the new-format shape
# (internal/declaration/payload.go). prepare reads the SAME payload a script check
# would.
payload="$(cat)"

# jq is this hook's one dependency beyond POSIX sh, and the whole plugin refuses
# rather than misbehaves without it (see check-rules.sh's header for the full
# argument). Checked by name here too, because prepare runs as its own process.
command -v jq >/dev/null 2>&1 || {
  echo "authoring-slop judge prepare: needs \`jq\`, which is not on PATH. Refusing — a check that cannot run must not be read as approval. Install jq, or disable sloprail/file-guard/authoring-slop." >&2
  exit 1
}

path="$(printf '%s' "$payload" | jq -r '.event.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  echo "authoring-slop judge prepare: the event named no path, so there is nothing to judge." >&2
  exit 1
fi

# A delete carries oldContent, never newContent, and has no resultKnown
# field either (events.md: "a delete has none — nothing remains") —
# judge.md.j2 interpolates event.newContent with no per-kind dispatch, so
# an undetected delete rendered an EMPTY <judged-file> block, and the model
# correctly reported it could verify nothing against primitive-usage rules
# — read by the engine as a refusal of a file that, by the time anyone
# could act on it, no longer exists to fix. There is nothing left to judge
# for primitive usage once a file is gone, so this abstains (skip: true)
# rather than asking the model to judge nothing.
kind="$(printf '%s' "$payload" | jq -r '.event.kind // empty' 2>/dev/null)"
case "$kind" in
  PreFileDelete|PostFileDelete)
    echo '{"skip": true}'
    exit 0
    ;;
esac

# The guard's own directory, so judge-rules/ resolves under it.
#
# NOT via SR_GUARDRAIL_DIR. The engine sets it on every check dispatch
# (internal/dispatch/exec.go), but it is not reliably ABSOLUTE — measured
# directly with a debug probe: a real dispatch carried
# SR_GUARDRAIL_DIR="marketplace/plugins/sloprail/.sloprail/file-guard/authoring-slop"
# (relative) while the process's own cwd was ALREADY that exact directory
# (this check, like every check, runs from the guard's own folder per
# authoring-guardrails/script-checks.md: "a script's command is resolved
# relative to that folder, and it runs with the folder as its working
# directory"). Joining a relative SR_GUARDRAIL_DIR onto that cwd doubles the
# path (.../authoring-slop/marketplace/plugins/sloprail/.sloprail/file-guard/
# authoring-slop/judge-rules/*/RULE.md — never exists), so the glob below
# silently matched nothing and every judge call refused with "judge-rules/
# contains no rule with enforced: true" regardless of the rules on disk —
# confirmed identical to check-rules.sh's OWN sibling script, which never hit
# this because it already resolves judge-rules/ relative to $0, not an env
# var. Matching that pattern here removes the double-prefix entirely; SR_
# GUARDRAIL_DIR is no longer read.
#
# SR_GUARDRAIL (the guard's NAME, not its path) still stands in for "was this
# dispatched by the engine" — absent means a hand-made invocation, and a judge
# that silently permitted then would be the "looks like a pass" failure this
# project has been burned by. Refuse loudly.
guardrail_dir="$(cd "$(dirname "$0")" && pwd)"
if [ -z "${SR_GUARDRAIL:-}" ]; then
  echo "authoring-slop judge prepare: SR_GUARDRAIL is unset, so this check was not dispatched by the engine. REFUSING." >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# Assemble the rules array. Ordering is LEXICAL BY DIRECTORY NAME (the `for`
# glob), so the rubric is a pure function of the directory's contents — two
# checkouts of the same tree produce the same array, and a diff of judge-rules/
# is a complete account of what changed. Rules are independent findings, so order
# carries no meaning beyond determinism.
#
# Each enforced rule becomes a jq object {name, body}. jq --arg keeps the body
# intact regardless of its punctuation (backslashes, ampersands, quotes), which a
# shell splice would mangle. The objects are collected with `jq -s` into one
# array at the end.
# ---------------------------------------------------------------------------
rules_json=""
count=0
for rf in "$guardrail_dir"/judge-rules/*/RULE.md; do
  [ -f "$rf" ] || continue

  # `enforced:` in the frontmatter decides whether a rule enters the rubric.
  # Presence of the file is NOT enough: a rule may be written for the next author
  # while being undecidable by this judge; without the flag the only way to record
  # it would be to leave it out of the repo.
  enforced="$(awk '
    NR==1 && $0=="---" { infm=1; next }
    infm && $0=="---"  { exit }
    infm && /^enforced:[[:space:]]*/ {
      sub(/^enforced:[[:space:]]*/, "")
      gsub(/[[:space:]]/, "")
      print; exit
    }
  ' "$rf" 2>/dev/null)"
  [ "$enforced" = "true" ] || continue

  name="$(basename "$(dirname "$rf")")"
  # Strip the frontmatter: it is bookkeeping for this loop, not a standard, and
  # sending it invites the model to treat `enforced:` as something to check.
  body="$(awk '
    NR==1 && $0=="---" { infm=1; next }
    infm && $0=="---"  { infm=0; next }
    !infm              { print }
  ' "$rf" 2>/dev/null)"
  [ -n "$body" ] || continue

  obj="$(jq -n --arg name "$name" --arg body "$body" '{name: $name, body: $body}')"
  rules_json="${rules_json}${obj}
"
  count=$((count + 1))
done

if [ "$count" -eq 0 ]; then
  # NOT fail-open — the deliberate asymmetry of a composed rubric, the same one
  # rule-quality keeps. A judge with no criteria cannot find a violation, so
  # permitting here is indistinguishable from a permanent clean bill of health.
  # Refuse instead — loud and immediately diagnosable.
  echo "authoring-slop judge prepare: judge-rules/ contains no rule with 'enforced: true', so there is no standard to judge '$path' against. REFUSING rather than judging against nothing — add a judge-rules/<name>/RULE.md, or disable this guardrail." >&2
  exit 1
fi

# Collect the per-rule objects into one array and emit it under
# additionalContext.rules — the one key a prepare may add
# (internal/dispatch/checks.go parsePreparedContext). The judge template renders
# {% for rule in additionalContext.rules %} over it.
rules_array="$(printf '%s' "$rules_json" | jq -s '.')"
jq -n --argjson rules "$rules_array" '{additionalContext: {rules: $rules}}'
