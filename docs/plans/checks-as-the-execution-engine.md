# If we migrated onto a10n's checks engine

**Status: NOT DOING THIS NOW.** Written down so it does not have to be
rediscovered. Nothing here is committed to; it is the shape the migration would
take, and the open questions that would have to be answered first.

## Why it comes up at all

sloprail needs a **prompt step**, and every judged rule here has hand-rolled one:
isolation (`cd /tmp`, emptied hooks/mcp/plugins), a timeout that must stay under
the engine's 30s, the model pin, a verdict tempfile a model will not "tidy up",
verdict parsing, and the fail-open/fail-closed split.

Five copies exist and they have already diverged in the way that matters:
`review-task.sh` parses its verdict with a greedy `{.*}` and is right to (its
verdict nests); `judge-skill.sh` uses a narrow `{[^{}]*}` and is right to (its
verdict is flat). Each is catastrophic in the other's rule, and skill-quality
shipped the wrong one — a verdict that had FLAGGED the file permitted it,
measured, in both object orders.

checks already owns all of that: `type: agent` with `model`, `messages`,
`template_file`, `validate_file`, plus `type: cache` and `group_by`. See
`spike/steps-format/FINDINGS.md` for the measured comparison.

## The five steps

### 1. Take checks

Its engine is `services/checks` in a10n: `steps` of `type: bash|cache|agent`,
`matcher`, `group_by`, JSONL rows of `{subject, kind, status, fingerprint}`.

### 2. Wire it to PreToolUse / Stop

Today checks is driven by a drain at session stop. sloprail needs it at two hook
points, and they ask different questions — see step 3.

### 3. Replace `matcher` with `expr` + `events`, or widen the subject

checks' matcher currently keys on ONE subject kind: a check-context link
(`a10n://spec/applications/<id>`, a regex with named groups feeding `group_by`).

sloprail's subjects are files and commands. Two shapes were considered:

```yaml
matcher:
  expr: glob("memories/tasks/*/*/TASK.md")
  events: [PreToolUse, Stop]
```

or, keeping the current shape and adding subject types beside `context:`:

```yaml
matcher:
  file: 'memories/tasks/(?P<group>[^/]+)/(?P<name>[^/]+)/TASK\.md'
  # later: command: ...
```

The second is smaller and keeps named groups feeding `group_by` for free. The
first is more uniform once commands arrive and a rule wants to combine a path
test with a marker test in one expression.

**Unresolved either way:** `group_by` over MARKERS. A path regex yields named
groups, so the subject is obvious. A file with three markers does not — is the
subject the file or the marker? Decide it against a real marker rule (unit 03),
not in the abstract.

### 4. Do not require commits — key the cache on ref + working-copy hash

This is the real blocker for reusing checks as-is, and it is narrower than it
first looks.

`changedFiles` in `services/spec/checkcmd_changedguard.go` already unions four
sources — `base..head`, `HEAD~1..HEAD`, `git diff HEAD` (uncommitted tracked),
and `git ls-files --others` (untracked). So the working tree IS seen.

What is NOT covered is sloprail's `Pre` question: *what is this toolcall about to
do*. The bytes are in the event (`content` on create, `result`+`resultKnown` on
update) and the write has not happened, so no git state answers it. That is
exactly where refusing is worth most — "the strongest Pre-vs-Post case in the
topic", per unit 17.

Good news for the cache: `fingerprint` is a FIELD THE BASH STEP EMITS
(`steps/cache.go` reads `stringField(row, "fingerprint")`), not a git ref the
engine computes. So keying on ref + working-copy hash — or on the pending bytes
at Pre — is a change to what a step emits, not to the engine's model.

### 5. Add a `skill` step

```yaml
steps:
  - type: skill
    skills: [document-task]
```

Covers `require-skill`: was this skill loaded in this session before the action.
It is a question about the SESSION, not about the file, which is why it is a step
type rather than something expressible in a matcher.

Listed as an array from the start because a path may plausibly demand more than
one, and because a single-valued field is the harder thing to widen later.

## What this does NOT settle, and would have to be decided first

**Are checks and sloprail one product or two?** If two, "use checks'
infrastructure" means sloprail depends on a10n — a release-and-ownership
decision, not a technical one. If one, the question is why there are two engines.
Everything above is moot until this is answered.

**Who owns fail-open policy.** Today each sloprail script decides and they have
diverged. Under `type: agent` the engine decides, with an override. That is a
behaviour change needing its own argument, separate from the format.

**What is lost with a declaration at all.** A matcher checked AT LOAD refuses a
rule naming a field its kind does not carry — a rule that would silently never
fire. If matching moves into `jq` over JSONL (the "we only do parsing" option,
also on the table), nothing can check it and a typo'd path is a rule that is
installed and silent forever. That is the one real argument for keeping a
declaration, and it survives independently of this migration.

## The alternative that is smaller

Extract only the judge machinery into a command — `sr judge --prompt X --validate
Y` — and leave today's format alone. It fixes the divergence that has already
bitten (five verdict parsers), needs no migration, and is useful whether or not
checks is ever adopted.

See also `spike/steps-format/` on `claude/steps-format-spike`.
