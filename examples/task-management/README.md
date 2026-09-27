# task-management (file-guard)

## The rule

An agent may append a result to a task; it may never edit the ask to match the
work. The ask changes only when the user changes it — adds scope, or drops it in
their own words ("forget X") — and every write to the ask must **cite** the human
message that authorised it: checked deterministically for existence, then by a
judge for truth and for containing **that and nothing else**. Deferring part of
the ask ("not today", "later") is not dropping it: the deferred part stays in the
ask, and what was done and what was deferred go in the result.

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
the ask guarded, turns "no uncited edit to the ask" back into a plain path rule
rather than needing a diff-region distinction the engine does not have.

`preventive: true`: an ungrounded edit to the ask must be refused **before** it
lands. A post-write refusal reports damage already done to the oracle, and
the agent's remedy would be to edit ASK.md again — another edit the user never
asked for.

## What the requirement and the judge divide

The citation rides on the write, never in the file, so ASK.md holds only the
ask:

```bash
sr-file write memories/tasks/auth/token-refresh/ASK.md \
  --cite:user 'refresh tokens before they expire' <<'ASK'
Refresh auth tokens before they expire.
ASK
```

1. **`require: [{citation: {source_types: [user]}}]` (first, no model):** the
   engine resolves every `--cite:user` quote against the session's record, and
   a write carrying none that resolves (a Write or Edit tool call, a shell
   redirect, a quote the user never said) is refused before any check runs.
   Unconditional, because ASK.md holds nothing but the ask.
2. **Prepare + judge:** `prepare` (`resolve-cited-messages.sh`) hands the judge
   the cited words off `event.citations`, each with where it sits in the
   record. The judge then answers what existence cannot: is the ask TRUE to
   those words, and does it hold **that and nothing else**? The "and nothing
   else" clause is the anti-slop half a naive implementation drops: a valid
   citation wrapped in agent-authored elaboration is still content slopped
   around a legitimate citation.

## What this guard protects against, precisely

Not carelessness — an asymmetry. The specification is simultaneously the
most valuable thing in the system and the thing an agent is most incentivised
to soften, because rewriting the ask to match the work makes every
downstream check pass without lying.
