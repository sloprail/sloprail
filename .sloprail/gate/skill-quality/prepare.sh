#!/usr/bin/env bash
# prepare: load this guard's meta-rules as structured data for the judge, from the
# guard's own rules/ directory — one entry per rules/<name>/RULE.md. Adding a
# meta-rule is adding a directory; no prompt is edited (the judge TEMPLATE
# iterates whatever array this emits). Emits the meta-rules as an ARRAY under
# additionalContext.meta_rules ([{name, body}, ...]); the judge-skill.md.j2 holds
# the rubric frame and renders the array with a {% for %} loop. The judge
# template reads the SKILL.md's own content directly off the flat event
# (event.newContent), so this prepare's ONE job is the meta-rule array.
#
# WHY A PREPARE AND NOT THE OLD HAND-ROLLED CLAUDE CALL. This guard was a SCRIPT
# check that assembled the prompt AND called `claude` itself, hand-rolling the
# model, the 25s timeout, the isolation --settings and the verdict parse. The
# reviewer asked for a judge: check so the engine (via sr-agent) owns all of
# that. What remains genuinely this guard's is loading the meta-rules — non-trivial
# (the enforced-only selection) — so it lives here, in prepare, and the model call
# is now the engine's.
#
# WHY AN ARRAY AND NOT A PRE-SPLICED STRING. This prepare used to read RUBRIC.md's
# frame and splice the meta-rules into a marker inside it, emitting the whole
# assembled blob as additionalContext.rubric. The reviewer asked for the frame to
# live in the template (visible, versioned as a prompt) and the rules to arrive as
# structured data. So the frame moved into judge-skill.md.j2 and this prepare now
# emits meta_rules as an array — no RUBRIC.md, no splice marker, no
# string-assembly. The template's {% for %} produces the same <meta-rule> blocks
# the splice once produced (gonja supports the loop; see judge.go/template.go).
#
# EXIT 0 with additionalContext on stdout: the meta-rules are loaded; the judge
# runs against them. EXIT 1: a REFUSAL — the check fails closed carrying this
# script's words (spec: a prepare failure fails the check). prepare has no third
# "permit without judging" outcome, which is the one behavioural change from the
# old script; see the FAIL-OPEN note below.
#
# FAIL-OPEN RECONCILIATION. The old script failed OPEN (permitted, unjudged) when
# its OWN machinery failed — no claude, a timeout, an unparseable verdict, a
# missing rules directory, an unreadable rule. A judge: check cannot reproduce
# that:
#   - MODEL machinery (no claude / timeout / bad verdict) is now the ENGINE's, and
#     the engine's judge path fails CLOSED on it by deliberate design
#     (internal/dispatch/judge.go). So those cases now REFUSE rather than permit.
#     Flagged, not hidden — the one behaviour a judge: check semantics cannot keep.
#   - RULE-LOADING machinery is this prepare's, and prepare has only refuse-closed
#     or proceed; there is no permit-without-judging. A missing rules/ dir or an
#     unreadable rule is the guard's OWN committed files broken, so refusing loudly
#     (via the empty-count refusal below) is the safe, diagnosable direction — a
#     change from the old fail-open, and flagged as such.
# EMPTY rules/ IS STILL A REFUSAL, not fail-open — the deliberate asymmetry of a
# composed rubric is preserved exactly (see "Nothing to judge against" below).

set -uo pipefail

# The CheckPayload the engine hands prepare on stdin: the file's own facts are
# FLAT under `.event` (`.event.path`, `.event.newContent`), the new-format shape
# (internal/declaration/payload.go). prepare reads the SAME payload a script check
# would.
payload="$(cat)"

path="$(printf '%s' "$payload" | jq -r '.event.path // empty' 2>/dev/null)"

# The guard's own directory, so rules/ resolves under it. The engine sets
# SR_GUARDRAIL_DIR on every check dispatch (internal/dispatch/exec.go).
guardrail_dir="${SR_GUARDRAIL_DIR:-}"

if [ -z "$path" ]; then
  echo "skill-quality: the event named no path, so there is nothing to judge" >&2
  exit 1
fi

# SR_GUARDRAIL_DIR absent means the payload did not come from the engine — a
# hand-made invocation. Without it rules/ cannot be found, and a judge that
# silently permitted then would be the "looks like a pass" failure this project
# has been burned by. Refuse loudly.
if [ -z "$guardrail_dir" ]; then
  echo "skill-quality: SR_GUARDRAIL_DIR is unset, so rules/ could not be located. REFUSING — this means the check was not dispatched by the engine." >&2
  exit 1
fi

# The content being judged, read FLAT off the event. The judge TEMPLATE reads
# this same field for the prompt; prepare reads it only to enforce the size gate
# below, so the two never disagree about what is judged. This is the GATE: it
# fires before the write (PreFileCreate/PreFileUpdate). A Pre write whose result
# the engine could not derive (resultKnown false: sed -i, an unresolvable sr-file
# line) carries newContent "" — indistinguishable from an emptied file — and
# judging "" would judge nothing. A gate does not fail closed on that by itself,
# so it refuses here, telling the agent to write the file content directly. The
# Stop file-guard of the same name judges the settled bytes instead.
if [ "$(printf '%s' "$payload" | jq -r '.event.resultKnown // false' 2>/dev/null)" != "true" ]; then
  echo "skill-quality: the result of this write to $path could not be derived (resultKnown false), so the skill could not be judged before it lands. Write the file's content directly (Write/Edit), or use sr-file on its own, instead of a command that edits it in place." >&2
  exit 1
fi
body="$(printf '%s' "$payload" | jq -r '.event.newContent // ""' 2>/dev/null)"

# A skill too large to judge is REFUSED, not permitted.
#
# The whole skill plus every meta-rule goes into the judge's prompt, so past
# roughly max_bytes the request exceeds the model's context and no verdict comes
# back. Under the OLD script that produced no verdict and the fail-open branch
# permitted an unjudged file (measured on the sibling: 2.1MB permitted in 3.8s).
# Size is caused by the content, identical on every run, and fixable — so it is
# refused here and named, BEFORE the model is ever asked.
#
# Threshold shared with the sibling judges: 534KB judged fine, 929KB rejected by
# the model.
max_bytes=600000
body_bytes="$(printf '%s' "$body" | wc -c | tr -d ' ')"
if [ -n "$body_bytes" ] && [ "$body_bytes" -gt "$max_bytes" ] 2>/dev/null; then
  cat >&2 <<EOF
SKILL QUALITY: $path is ${body_bytes} bytes, which is too large to judge (the
whole skill goes into the judge's prompt, and past roughly ${max_bytes} bytes
the request exceeds the model's context and no verdict comes back).

Refused rather than permitted because the size is itself the finding: a skill
this large is not one an agent can load and act on. Split it into the skill and
its reference files, and it will be judged normally.
EOF
  exit 1
fi

# ---------------------------------------------------------------------------
# Load the meta-rules into a JSON array: one {name, body} entry per
# rules/<name>/RULE.md that is enforced.
#
# Ordering is LEXICAL BY DIRECTORY NAME, which is what the `for` glob gives.
# Not by mtime, not by a manifest: the array must be a pure function of the
# directory's contents, so two checkouts of the same tree produce the same
# prompt and a diff of rules/ is a complete account of what changed.
#
# Built with jq rather than by shell string-concatenation because the bodies
# contain arbitrary punctuation — backslashes, ampersands, quotes, angle
# brackets — and jq --arg encodes each as a JSON string safely, where a
# hand-built string would need escaping the template then has to undo.
# ---------------------------------------------------------------------------
meta_rules='[]'
count=0
for rf in "$guardrail_dir"/rules/*/RULE.md; do
  [ -f "$rf" ] || continue

  # `enforced:` in the frontmatter decides whether a meta-rule enters the rubric.
  # Presence of the file is NOT enough: a meta-rule may legitimately be written
  # down for the next author while being undecidable by a judge, and without the
  # flag the only way to record such a rule would be to leave it out of the repo.
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
  # Strip the frontmatter: it is bookkeeping for this loop, not standard, and
  # sending it invites the model to treat `enforced:` as something to check.
  content="$(awk '
    NR==1 && $0=="---" { infm=1; next }
    infm && $0=="---"  { infm=0; started=1; next }
    !infm              { print }
  ' "$rf" 2>/dev/null)"

  [ -n "$content" ] || continue

  # Append {name, body} to the array. jq --arg encodes both as JSON strings, so
  # the body's own punctuation cannot break the structure.
  meta_rules="$(jq -c --arg name "$name" --arg body "$content" \
    '. + [{name: $name, body: $body}]' <<<"$meta_rules")" || {
    # jq itself failing is the loader's machinery breaking around a rule that
    # exists — refuse closed rather than silently drop the rule (which would judge
    # against a smaller standard than the tree actually declares).
    echo "skill-quality: failed to encode meta-rule '$name' as JSON, so the rubric could not be assembled. REFUSING (the guard's own rule file could not be loaded)." >&2
    exit 1
  }
  count=$((count + 1))
done

if [ "$count" -eq 0 ]; then
  # NOT fail-open, and this is the deliberate asymmetry of a composed rubric,
  # PRESERVED EXACTLY from the old script.
  #
  # Every other failure here is the machinery breaking around a standard that
  # still exists. This one is the standard being absent: the guardrail is
  # declared, it fires, it loads — and it would judge against nothing, which is
  # the inert-but-official-looking rule sloprail exists to prevent. A judge with
  # no criteria cannot find a violation, so permitting here is indistinguishable
  # from a permanent clean bill of health. Refuse instead — loud and immediately
  # diagnosable. (A missing rules/ dir or an unreadable rule lands here too: the
  # glob matches nothing, count stays 0 — fail-closed, the old fail-open's
  # replacement for the rule-loading machinery.)
  echo "skill-quality: rules/ contains no meta-rule with 'enforced: true', so there is no standard to judge '$path' against. REFUSING rather than judging against nothing — add a rules/<name>/RULE.md, or disable this guardrail." >&2
  exit 1
fi

# Emit the meta-rules under additionalContext.meta_rules, the ONE key a prepare
# may add (internal/dispatch/checks.go parsePreparedContext). The judge template
# iterates {% for r in additionalContext.meta_rules %} to render each into the
# rubric frame it holds; --argjson keeps the array structured rather than
# re-stringifying it.
jq -n --argjson meta_rules "$meta_rules" '{additionalContext: {meta_rules: $meta_rules}}'
