# no-unasked-deletion (file-guard)

**Unit:** [17_no-unasked-deletion](/Users/nsviridenko/ws/sloprail/strategy/memories/topics/20260812_no-slop/units/17_no-unasked-deletion/UNIT.md)
**Nature:** file-guard ([decision 20260818_no-slop-primitives](/Users/nsviridenko/ws/sloprail/strategy/memories/decisions/20260818_no-slop-primitives/DECISION.md) — grounding #05 / reconciliation #07, expressed as a file-guard's checks)

## The rule

An edit must not silently drop content nobody asked to remove. Append instead
of rewriting; a "crazy rewrite" that quietly destroys information is a
violation *regardless of whether the new text is good* — the replacement being
better prose is what makes it slop rather than a bug. The load-bearing word is
**unasked**: the rule is about the relationship between a removal and the
human's own trajectory messages, not about removal itself.

It exists because of a real incident — a 27-line `TOPIC.md` overwritten
wholesale, destroying its provenance link and the author's scope wording, with
no deletion ever requested.

## Why a file-guard, and why `preventive`

Bound to a file's state (`PreFileUpdate`), `preventive: true`. The content that
would be lost is still on disk at Pre time, so the diff is computed *before* the
loss. A `Post` version could only report a deletion already done — and the
natural remedy, restore from git, is gone if the file was never committed. This
is the strongest pre-vs-post case in the topic.

`newContent` is optional on `PreFileUpdate` (a `sed -i` or an env-dependent
command's result cannot be precomputed). The guard **fails closed** when it is
absent: a write whose result cannot be shown to preserve content is refused,
not waved through — the same fail-open/fail-closed logic the incident came from
(a check that could not run has established nothing).

## The parts — cheap gates expensive (same shape as unit 16)

- **`removed-lines-were-asked-for.sh`** (script) — the deterministic half,
  "worth shipping before the judge." A line-by-line diff of old vs new:
  - `newContent` absent → **block** (fail-closed).
  - no removed lines → **pass**: pure additions is "append, not rewrite",
    always fine.
  - removed lines, and no deletion/rewrite intent in this turn's human messages
    → **block outright** — an unasked removal, decided deterministically (this
    is what would have caught the incident).
  - removed lines, but a human *did* voice a deletion/rewrite → **pass to the
    judge**: whether that (often loose) ask covers *these* lines is the one
    question a model is for.

  The "did anyone ask" check reads the human messages off the normalized
  trajectory (`sr-session trajectory normalize`, `type == "user"`) — the same
  ground-truth move as unit 16, and the first rule-side use of user-message
  search that Thread 1's commands make clean.

- **`collect-removed-and-asks.sh`** (prepare) + **`loose-ask-covers-removal.md.j2`**
  (judge) — reached only for the residue. `prepare` hands the judge exactly the
  removed lines and the human messages (under `additionalContext`); the judge
  rules whether a loose ask ("clean this up", "rewrite it properly") authorized
  dropping *this specific* content, and refuses when a general improvement
  request is being used to justify destroying provenance, a decision, or a fact.

## In tension with unit 07, by design

Unit 07 (one fact, one home) *demands* removing duplication; unit 17 refuses
removing what nobody asked to lose. The resolution (named in the unit): 07
authorizes a specific class of deletion — a fact that demonstrably still lives
elsewhere — and 17 refuses the rest. A deletion under 07 should have to name the
surviving home; that naming is the ask 17 looks for.
