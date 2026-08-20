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

## "Asked" is a grounded quote-marker, not a keyword grep

A first build grepped this turn's human messages for deletion words
(`delete|remove|rewrite|clean up`). That was rejected as itself heuristic — the
same fluent-guess disease this unit is about (it misses asks worded differently,
false-passes on the word appearing unrelated). The reworked mechanism makes
"asked" **deterministic and grounded**, and needs no registry file:

- The agent writes the **quote** of what the user asked as a marker in the
  file's frontmatter — a YAML comment the marker extractor reads:

  ```md
  ---
  # sr:asked "keep the original transcript_path, just add the new section"
  ---
  ```

  (Comment-only frontmatter is valid YAML — comments parse to an empty document
  — so the marker rides there even before the file has real frontmatter fields.)

- The check **grounds the quote with `sr-session trajectory cite`**: it must
  resolve to the user's own words in the trajectory — a message OR an
  AskUserQuestion answer. A quote that resolves nowhere is a fabricated ask.

## The parts — cheap gates expensive (same shape as unit 16)

- **`removal-has-a-grounded-ask.sh`** (script) — the deterministic half. A
  line-by-line diff of old vs new:
  - `newContent` absent → **block** (fail-closed).
  - no removed lines → **pass**: pure additions is "append, not rewrite".
  - removed lines, no `sr:asked` marker → **block** (unasked removal — the
    incident).
  - removed lines, marker present but its quote does not resolve via `cite` →
    **block** (fabricated / paraphrased ask, not the user's words).
  - removed lines, marker quote **resolves** → **pass to the judge**.

- **`collect-quote-and-diff.sh`** (prepare) + **`change-is-clean-and-absolute.md.j2`**
  (judge) — reached only when a real removal has a grounded ask. The judge rules
  the two things only a model can: (1) the change is **clean and targeted** —
  only what the quote asked, nothing else dropped alongside it; and (2) it is
  **absolute, not a delta** — the content states the final truth, it does not
  narrate "the user meant X, not Y" or keep the old value as commentary (the
  diff's job is to show the change, not the file's).

This is the first rule-side use of user-word search that Thread 1's `cite`
makes clean — and the minimal core of the larger end-to-end-proof direction the
unit now drafts (invariant ← quote ← test-case ← run-result ← tool-call,
composed by a CLI into a deterministic, reference-only report).

## In tension with unit 07, by design

Unit 07 (one fact, one home) *demands* removing duplication; unit 17 refuses
removing what nobody asked to lose. The resolution (named in the unit): 07
authorizes a specific class of deletion — a fact that demonstrably still lives
elsewhere — and 17 refuses the rest. A deletion under 07 should have to name the
surviving home; that naming is the ask 17 looks for.
