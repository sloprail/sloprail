# content-de-layering

**Unit:** [07_content-de-layering](/Users/nsviridenko/ws/sloprail/strategy/memories/topics/20260812_no-slop/units/07_content-de-layering/UNIT.md)
**Nature:** file-guard ([decision 20260818_no-slop-primitives](/Users/nsviridenko/ws/sloprail/strategy/memories/decisions/20260818_no-slop-primitives/DECISION.md), slice 2, candidate list)

## The rule

Right content in the right file: an update references a person file, it
doesn't duplicate it inline; no strategy reasoning leaks into branding;
one-fact-one-home. Purely a judge call — there is no mechanical signature of
"this is a duplicate", only a reading of whether a fact stated here actually
belongs to this file's subject.

## Why file-guard

The guard is about the file's own content being right, independent of
whether this write created it or merely modified it — a file that
accumulated a duplicated fact over several unrelated edits is exactly as
wrong as one that got it in a single write. No `preventive` here: there is no
cheap, reliable way to predict at Pre time whether new prose duplicates a
fact that lives elsewhere, so this guard is after-only.

## Why judge-only, no script tier

Unlike the other file-guard examples in this set, this rule has no
deterministic first cut. "Is this fact duplicated, or does it natively
belong here" is not a byte-comparable property — there is no string or
line-count signature that separates a legitimate reference from an
illegitimate restatement. The whole check is the judge.

## The tension this unit names on purpose

In direct tension with unit 17 (no-unasked-deletion) by design: 17 refuses
removing content nobody asked to remove; this unit actively demands removing
a specific class of content — a fact that demonstrably still lives
elsewhere. The resolution lives in the judge's answer: a de-layering removal
is only a pass if it names the surviving home the fact still lives in. A
removal that cannot point at where the fact remains is not de-layering, it's
unasked deletion wearing this unit's justification.
