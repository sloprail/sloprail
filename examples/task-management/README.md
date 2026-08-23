# task-management

**Unit:** [16_task-management](/Users/nsviridenko/ws/sloprail/strategy/memories/topics/20260812_no-slop/units/16_task-management/UNIT.md)
**Nature:** file-guard ([decision 20260818_no-slop-primitives](/Users/nsviridenko/ws/sloprail/strategy/memories/decisions/20260818_no-slop-primitives/DECISION.md), slice 2, candidate list)

## The rule

An agent may append a result to a task; it may never edit the ask. Task
content must carry a reference to the human message that authorised it —
checked deterministically for presence, then by a judge for truth and for
containing **that and nothing else**.

This is the sharpest form of the no-slop thesis, because every other unit
protects an artifact — this one protects the **oracle**, the thing the other
checks are checked against. The failure it prevents has no detector once it
happens: an agent implements 70% of an ask, edits the task to describe that
70%, and from then on every verification passes — the work matches the spec,
because the spec was rewritten to match the work.

## Why file-guard, and the structural split behind it

The guard binds `ASK.md` specifically — not the whole task folder — because
the engine has no notion of "which region of a file changed", only that a
path changed. Splitting the ask and the result into separate files, with only
the ask guarded, turns "don't edit the ask" back into a plain path rule
rather than needing a diff-region distinction the engine does not have.

`preventive: true`: an edit to task content must be refused **before** it
lands. A post-write refusal reports damage already done to the oracle, and
the agent's remedy would be to edit ASK.md again — the exact prohibited act.

## What the two checks divide

1. **Script (cheap, first):** does the content carry ANY reference to a human
   message at all — a transcript_path, message id, or line range? No
   reference, no model needed: refuse outright.
2. **Prepare + judge (only once a reference exists):** `prepare`
   (`resolve-referenced-message.sh`) resolves the reference against
   `transcriptPath` and hands the judge template the actual message text as
   a variable — the template never parses a transcript itself. The judge then
   answers what a script cannot: is the reference TRUE — does the content
   correspond to what the human actually said there — and does the content
   hold **that and nothing else**? The "and nothing else" clause is the
   anti-slop half a naive implementation drops: a valid reference wrapped in
   agent-authored elaboration is still content slopped around a legitimate
   citation.

## What this guard protects against, precisely

Not carelessness — an asymmetry. The specification is simultaneously the
most valuable thing in the system and the thing an agent is most incentivised
to soften, because rewriting the ask to match the work makes every
downstream check pass without lying.
