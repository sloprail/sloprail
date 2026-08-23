# The judge's rules registry — primitive usage a guardrail must get right

This directory is the standard the `judge:` check in `file-guard.yaml` reasons
against. Each rule is `judge-rules/<name>/RULE.md`, one mistake and its fix, with
`enforced: true` in the frontmatter to enter the assembled rubric — the same
layout the sibling `rule-quality` guard uses for its `rules/`.

`prepare.sh` assembles the enforced rules into an ARRAY and hands it to the judge
as `additionalContext.rules`; `judge.md.j2` iterates that array to frame the
prompt. Adding a rule is adding a directory; no prompt is edited.

## Why a separate directory from `rules/`

`../rules/` is the GREP's documentation — one deterministic grep in
`check-rules.sh` per enforced rule there, plus the "how the check detects it"
prose. This `judge-rules/` is the JUDGE's registry: the rules that need
REASONING, not a signature. Keeping them apart means the judge is never asked
against grep-detection prose, and the grep's notes never drift into the model's
rubric.

## Grep vs judge — the split

The cheap script (`check-rules.sh`) runs first; the judge second. They are
complementary, not redundant:

- **Grep** (`../rules/`): exact signatures — two tool names piped, `newContent`
  with no `resultKnown` named anywhere, a model invocation with no DATA clause.
  Cheap and certain; fires only where a human would not disagree.
- **Judge** (here): the reasoning above the signatures — the derivable-*create*
  assumption the grep can't see (a script that names `resultKnown` but still reads
  `newContent` on `PreFileCreate`), a `.md.j2` prompt that interpolates content
  without a delimiter, a field read from the wrong place per kind, a ledger
  written to `$PWD`, an `sr-agent` spawn dropping `SLOPRAIL_LAUNCHED_BY`.

Two rules appear in both registries by design — `content-may-be-unresolvable`
(grep: field missing) vs `pre-kinds-consult-resultknown` (judge: wrong branch),
and `judged-content-is-data` (grep: script invocation) vs the judge version
(prompt-file reasoning), and `prefer-file-events-over-trajectory` (grep:
`|`-alternation) vs the judge version (the approach). The grep is the floor; the
judge is what the floor cannot reach.

## The authoritative "what's available" source

These rules ENFORCE what the **authoring-guardrails skill** teaches — the file
events and their per-kind fields, the guardrail environment variables, the check
contracts, and the `resultKnown` discipline. The skill is the authoritative
reference for "what primitive is available and when to use it"; this registry is
the enforcement side of the same knowledge. When the two disagree, the skill's
current text and the engine (`internal/filemod/module.go`,
`services/sr-session/nature_fileguard.go`, `internal/dispatch/exec.go`) are the
ground truth — update the rule to match, not the reverse.
