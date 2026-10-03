# no-unasked-deletion (gate + file-guard)

An edit must not silently drop content nobody asked to remove — appending
instead of rewriting, since a "better" rewrite that quietly destroys
information is still a violation.

## Install

Make sure sloprail is installed — see the docs
[install page](https://sloprail.com/docs/getting-started/install/).

Then copy this example into your project:

```bash
git clone --depth 1 https://github.com/sloprail/sloprail /tmp/sloprail-clone && mkdir -p .sloprail && cp -R /tmp/sloprail-clone/examples/no-unasked-deletion/.sloprail/. .sloprail/ && rm -rf /tmp/sloprail-clone
```

Then rewrite a file so it drops content nobody asked to remove, and confirm
you see the refusal below.

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

## Why a gate, and a file-guard of the same name

The rule ships as two folders with one name, split by what each is for.

The **gate** (`gate/preserves-unasked-content`) is the prevention, and it is cheap:
no model runs in it. It triggers on `PreFileWrite` (a create or an update) and
`PreFileDelete` of a `memories/*.md` file, and refuses — before the loss, while the
content is still on disk — a change that removes content without the user's words
cited (`require: citation`, `when` `removes-content.sh` says the change removes
something). A check at Stop could only report a deletion already done, and the
natural remedy, restore from git, is gone if the file was never committed.

The plain **file-guard** (`file-guard/preserves-unasked-content`) is the
after-check, and it holds the **judge**. It judges **commits**: at Stop, uncommitted
changes to a `memories/` file refuse the turn with "commit these", and the rule then
is judged by `sr-checks run` over the range `merge-base(base, HEAD)..HEAD`, as one squashed diff of every
file it touched (the net diff is judged: a removal a later commit put back is not a removal), with the same
`deletions: include` and `require`: it asks whether the removal is clean and asked
for. The user's words come from the commits' `Sloprail-Cites-User: <quote>` trailers.
It sees what the gate cannot — a removal made by a script the engine did not see as a
write. The judge belongs here because a judge is a model call: the gate stays
deterministic, the file-guard rules on the result.

One library per script, in the file-guard folder (`<script>-lib.sh`), holds the shared
logic; each half keeps a thin entry that reads its own input (the gate's `Pre*` event,
the file-guard's Changeset, committed content that is always known) and sources it.

`resultKnown` is false on a create or update whose result the engine could not
precompute (a `sed -i`, or `sr-file` mixed into a longer command line), and then
`newContent` is empty — indistinguishable from an emptied file. The gate **fails
closed**: `require-known-result.sh` runs first and refuses such a write, so a
write that cannot be shown to preserve content is refused, not waved through — a
check that could not run has established nothing.

The gate has a `PreFileDelete` trigger, and the file-guard `deletions: include`,
because deleting a memory file is the whole-file form of the same loss. A
file-guard skips deleted files by default and a gate only sees what it triggers
on; this rule opts in on both, so a deletion reaches it and needs a citation like
any other removal: `rm memories/x.md` cites nothing and is refused; `sr-file
delete memories/x.md --cite:user '<quote>'` passes the gate and is judged by `sr-checks run` and verified at Stop. The gate is asked
about every file a command deletes, so `rm a.md b.md` is refused naming both.

A delete whose bytes the engine did not read — `oldContentKnown: false`, for a
file past a recursive removal's read budget, larger than a delete read, or not a
regular file — still needs the citation, and **always** goes to the judge: an
empty `oldContent` there is "not read", not "nothing removed". The prepare used
to read it as nothing removed and skip the judge, so any resolvable quote of the
user's admitted `rm -rf` of a memory the engine had not read. The file-guard has no such case: it reads committed
blobs, which are always known, and a deleted file's `oldContent` is what the range's
base held.

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
answers — and delivers it on `.event.citations` (to the gate). The file-guard takes
the same grounding from the commit, as a `Sloprail-Cites-User: <quote>` trailer,
resolved the same way and delivered on `changeset.citations`. A quote that resolves nowhere
is not a citation at all, so a fabricated or paraphrased ask cites nothing.
`sr-file` runs on its own line so its result can be computed before it runs;
mixed into a longer command, the result is unknown and refused (see above).

The requirement is declared on the rule, conditionally — a pure append asks
nothing and needs no citation:

```yaml
require:
  - citation: {source_types: [user]}
    when: ./removes-content.sh
```

## The parts — cheap gates expensive

- **`require-known-result.sh`** (gate only, script check) — refuses a create or
  update whose result the engine could not compute, before anything reads the
  empty `newContent`.

- **`removes-content.sh`** (`when`) — the deterministic half. A line-by-line
  diff of old vs new decides whether the citation applies:
  - no removed lines → **waived**: pure additions is "append, not rewrite"
    (over the squashed range: a line removed by one commit and put back by
    another is not removed).
  - removed lines, or a deletion → **applies**: an uncited removal is refused by
    the engine before the judge is paid for (the incident; a fabricated quote
    never became a citation).
  - a result the engine could not compute → **applies** (fail-closed).

- **`skip-pure-addition.sh`** (prepare) + **`change-is-clean-and-absolute.md.j2`**
  (judge), **file-guard only** — reached at Stop only when a real removal cites the user's words (the
  prepare skips the judge on a pure addition — never on a delete whose bytes
  were not read). The judge reads the change's
  diff and its citations straight off its input, and
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
