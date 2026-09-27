# no-unasked-deletion (file-guard)

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
natural remedy, restore from git, is gone if the file was never committed.

`newContent` is optional on `PreFileUpdate` (a `sed -i`, or `sr-file` mixed
into a longer command line, has a result that cannot be precomputed). The guard
**fails closed** when it is absent: a write whose result cannot be shown to
preserve content is refused, not waved through — a check that could not run has
established nothing.

`deletions: include`, because deleting a memory file is the whole-file form of
the same loss. A file-guard skips deleted files by default; this one opts in, so
a deletion reaches it as a `PreFileDelete` and needs a citation like any other
removal: `rm memories/x.md` cites nothing and is refused; `sr-file delete
memories/x.md --cite:user '<quote>'` goes to the judge.

## "Asked" is a cited quote, not a keyword grep

Grepping the turn's human messages for deletion words
(`delete|remove|rewrite|clean up`) is itself heuristic — it misses asks worded
differently and false-passes on the word appearing unrelated. Instead, "asked"
is made **deterministic and grounded**: a removal must **cite** the user's own
words, on the command that makes it — never in the file, which keeps only its
own content:

```bash
sr-file edit memories/runbook.md --old-string '<old>' --new-string '<new>' \
  --cite:user 'drop the old rollback steps'
```

Before the check runs, the engine resolves the quote against the session's
record — it must land on exactly one of the user's messages or AskUserQuestion
answers — and delivers it on `.event.citations`. A quote that resolves nowhere
is not a citation at all, so a fabricated or paraphrased ask cites nothing.
`sr-file` runs on its own line so its result can be computed before it runs;
mixed into a longer command, the result is unknown and refused (see above).

It is a script check, not `require: [{citation: true}]`, because the need is
conditional: a pure append asks nothing and needs no citation.

## The parts — cheap gates expensive

- **`removal-has-a-grounded-ask.sh`** (script) — the deterministic half. A
  line-by-line diff of old vs new:
  - `newContent` absent → **block** (fail-closed).
  - no removed lines → **pass**: pure additions is "append, not rewrite".
  - removed lines (or a deletion), no citation of the user's words → **block**
    (unasked removal — the incident; a fabricated quote never became a citation).
  - removed lines (or a deletion) citing the user's words → **pass to the judge**.

- **`collect-quote-and-diff.sh`** (prepare) + **`change-is-clean-and-absolute.md.j2`**
  (judge) — reached only when a real removal cites the user's words. The judge
  rules the two things only a model can: (1) the change is **clean and
  targeted** — only what the cited words asked, nothing else dropped alongside
  it; and (2) it is
  **absolute, not a delta** — the content states the final truth, it does not
  narrate "the user meant X, not Y" or keep the old value as commentary (the
  diff's job is to show the change, not the file's).

## In tension with de-duplication, by design

A "one fact, one home" rule *demands* removing duplication; this rule refuses
removing what nobody asked to lose. The resolution: de-duplication authorizes a
specific class of deletion — a fact that demonstrably still lives elsewhere —
and this rule refuses the rest. Such a deletion should have to name the
surviving home; that naming is the ask this rule looks for.
