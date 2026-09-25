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

`newContent` is optional on `PreFileUpdate` (a `sed -i` or an env-dependent
command's result cannot be precomputed). The guard **fails closed** when it is
absent: a write whose result cannot be shown to preserve content is refused,
not waved through — a check that could not run has established nothing.

`deletions: include`, because deleting a memory file is the whole-file form of
the same loss. A file-guard skips deleted files by default; this one opts in, so
an `rm memories/x.md` reaches it as a `PreFileDelete` — which carries no
`newContent`, so it is refused on the same fail-closed path.

## "Asked" is a grounded quote-marker, not a keyword grep

Grepping the turn's human messages for deletion words
(`delete|remove|rewrite|clean up`) is itself heuristic — it misses asks worded
differently and false-passes on the word appearing unrelated. Instead, "asked"
is made **deterministic and grounded**, needing no registry file:

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

## The parts — cheap gates expensive

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

## In tension with de-duplication, by design

A "one fact, one home" rule *demands* removing duplication; this rule refuses
removing what nobody asked to lose. The resolution: de-duplication authorizes a
specific class of deletion — a fact that demonstrably still lives elsewhere —
and this rule refuses the rest. Such a deletion should have to name the
surviving home; that naming is the ask this rule looks for.
