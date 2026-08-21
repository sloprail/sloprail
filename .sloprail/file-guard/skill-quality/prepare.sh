#!/usr/bin/env bash
# prepare: assemble the rubric this judge is asked against, from the guard's own
# rules/ directory — one meta-rule per rules/<name>/RULE.md, spliced into
# RUBRIC.md's frame. Adding a meta-rule is adding a directory; no prompt is
# edited. Emits the assembled rubric as additionalContext.rubric, which the
# .md.j2 interpolates; the judge template reads the SKILL.md's own content
# directly off the flat event (event.newContent), so this prepare's ONE job is
# the rubric.
#
# WHY A PREPARE AND NOT THE OLD HAND-ROLLED CLAUDE CALL. This guard was a SCRIPT
# check that assembled the prompt AND called `claude` itself, hand-rolling the
# model, the 25s timeout, the isolation --settings and the verdict parse. The
# reviewer asked for a judge: check so the engine (via sr-agent) owns all of
# that. What remains genuinely this guard's is the rubric assembly — non-trivial
# (the enforced-only selection, the frame/splice) — so it lives here, in prepare,
# and the model call is now the engine's.
#
# EXIT 0 with additionalContext on stdout: the rubric is assembled; the judge
# runs against it. EXIT 1: a REFUSAL — the check fails closed carrying this
# script's words (spec: a prepare failure fails the check). prepare has no third
# "permit without judging" outcome, which is the one behavioural change from the
# old script; see the FAIL-OPEN note below.
#
# FAIL-OPEN RECONCILIATION. The old script failed OPEN (permitted, unjudged) when
# its OWN machinery failed — no claude, a timeout, an unparseable verdict, a
# missing RUBRIC.md, a lost splice marker. A judge: check cannot reproduce that:
#   - MODEL machinery (no claude / timeout / bad verdict) is now the ENGINE's, and
#     the engine's judge path fails CLOSED on it by deliberate design
#     (internal/dispatch/judge.go). So those cases now REFUSE rather than permit.
#     Flagged, not hidden — the one behaviour a judge: check semantics cannot keep.
#   - RUBRIC/MARKER machinery is this prepare's, and prepare has only refuse-closed
#     or proceed; there is no permit-without-judging. A missing RUBRIC.md or lost
#     marker is the guard's OWN committed file broken, so refusing loudly (below)
#     is the safe, diagnosable direction — a change from the old fail-open, and
#     flagged as such.
# EMPTY rules/ IS STILL A REFUSAL, not fail-open — the deliberate asymmetry of a
# composed rubric is preserved exactly (see "Nothing to judge against" below).

set -uo pipefail

# The CheckPayload the engine hands prepare on stdin: the file's own facts are
# FLAT under `.event` (`.event.path`, `.event.newContent`), the new-format shape
# (internal/declaration/payload.go). prepare reads the SAME payload a script check
# would.
payload="$(cat)"

path="$(printf '%s' "$payload" | jq -r '.event.path // empty' 2>/dev/null)"

# The guard's own directory, so RUBRIC.md and rules/ resolve under it. The engine
# sets SR_GUARDRAIL_DIR on every check dispatch (internal/dispatch/exec.go).
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
# below, so the two never disagree about what is judged. A Post event carries the
# settled bytes in newContent too (internal/filemod/extract.go KindPostCreate).
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
# Assemble the rubric: RUBRIC.md's frame, with <<<META_RULES>>> replaced by the
# concatenated rules/<name>/RULE.md.
#
# Ordering is LEXICAL BY DIRECTORY NAME, which is what the `for` glob gives.
# Not by mtime, not by a manifest: the rubric must be a pure function of the
# directory's contents, so two checkouts of the same tree produce the same
# rubric and a diff of rules/ is a complete account of what changed.
# ---------------------------------------------------------------------------
frame=""
if [ -f "$guardrail_dir/RUBRIC.md" ]; then
  frame="$(grep -v '^#' "$guardrail_dir/RUBRIC.md" 2>/dev/null)"
fi

if [ -z "$frame" ]; then
  # Old behaviour was FAIL-OPEN here (permit unjudged). A judge: check's prepare
  # cannot permit-without-judging, and RUBRIC.md is the guard's OWN committed
  # file — a missing one is a real breakage, so this refuses loudly. Flagged as
  # a change from the old fail-open in the header note.
  echo "skill-quality: could not read RUBRIC.md, so there is no rubric frame to judge '$path' against. REFUSING (the guard's own rubric file is missing or empty)." >&2
  exit 1
fi

case "$frame" in
  *'<<<META_RULES>>>'*) ;;
  *)
    # The frame is present but lost its splice point, so the meta-rules would be
    # silently dropped and the judge would be asked against a frame that says
    # "below are the meta-rules" with nothing below it. Old behaviour: fail-open.
    # New: refuse.
    echo "skill-quality: RUBRIC.md has no <<<META_RULES>>> marker, so the meta-rules could not be spliced in. REFUSING (the guard's own rubric frame is malformed)." >&2
    exit 1
    ;;
esac

meta=""
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

  meta="${meta}<meta-rule name=\"${name}\">
${content}
</meta-rule>

"
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
  # diagnosable.
  echo "skill-quality: rules/ contains no meta-rule with 'enforced: true', so there is no standard to judge '$path' against. REFUSING rather than judging against nothing — add a rules/<name>/RULE.md, or disable this guardrail." >&2
  exit 1
fi

# Splice. awk rather than a shell parameter expansion because the meta-rules
# contain backslashes and ampersands (sed would reinterpret them).
rubric="$(META="$meta" awk '
  index($0, "<<<META_RULES>>>") { print ENVIRON["META"]; next }
  { print }
' <<<"$frame")"

# Emit the assembled rubric under additionalContext.rubric, the ONE key a prepare
# may add (internal/dispatch/checks.go parsePreparedContext). The judge template
# renders {{ additionalContext.rubric }} into the prompt; jq -Rs keeps the whole
# rubric intact regardless of its punctuation.
jq -n --arg rubric "$rubric" '{additionalContext: {rubric: $rubric}}'
