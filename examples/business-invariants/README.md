# business-invariants (file-guard + gate)

Code that claims to implement a business invariant carries a marker naming
it; a judge checks the marked code actually upholds that invariant, and that
the marker still points at the version of the spec it was written against.

## Install

Make sure sloprail is installed — see the docs
[install page](https://sloprail.com/docs/getting-started/install/).

Then copy this example into your project:

```bash
git clone --depth 1 https://github.com/sloprail/sloprail /tmp/sloprail-clone && mkdir -p .sloprail && cp -R /tmp/sloprail-clone/examples/business-invariants/.sloprail/. .sloprail/ && rm -rf /tmp/sloprail-clone
```

Then change code under a marker so it no longer upholds the invariant it
names, and confirm you see the refusal below.

## The rule

Invariant files live in the codebase; the code carries MARKERS naming each
one; a judge verifies the marked code actually upholds the business invariant
it claims to. This is business logic, not shape.

## The addition: pin the marker to a spec version

A marker naming an invariant says WHICH invariant, but not WHICH VERSION of
it. The spec can be reworded, narrowed, or reversed after the marker was
written, and nothing notices — the marker still points at the invariant's
name, the invariant still exists, and the pair has quietly stopped meaning
what it meant when the code was written.

The fix: the marker's fqn carries a GitHub-shaped link pinned to a commit —
repo, sha, path, line range. The sha buys two things:

- **A reference to compare against.** The judge rules against the exact text
  the code was written against, not whatever the spec says today.
- **A change signal.** Diffing the pinned range against HEAD is checkable by a
  script, before any judge runs — if the spec moved and the marker did not,
  that is a fact, not an impression.

## Why file-guard (pinned-invariant)

The rule is about the file's state — "does this marker's code still hold" —
not about the event that touched it. A marker that starts failing because the
spec moved underneath it stays failing until either the code catches up or the
marker is re-pinned: the file-guard judges the **commits** the rule has not yet
passed, and a refused range is never partly passed, so the fix is judged together
with the commit it fixes. That is why `pinned-invariant` is a plain file-guard
with no gate: nothing about the code has to be refused before it is written, only
judged once it is committed. The second rule below is the opposite case.

A file-guard needs the work **committed**: at Stop, uncommitted changes to a file
the rule selects refuse the turn with "commit these" (nothing is committed for the
agent), and the rule then runs on the range from where it last passed to `HEAD`.
Its checks read that range as a Changeset (`.changeset.files[]`, each with
`oldContent`, `newContent`, `oldMarkers`, `newMarkers`) and a read-only snapshot of
head (`$SR_TREE`); a script loops over the files.

## What the two checks divide

1. **Script (cheap, first):** does the pin even resolve (real commit, real
   path, real line range), and does the pinned text still match HEAD? Pure
   byte comparison — no model needed to catch spec drift. The fqn is written
   by the agent being judged, so `pin.sh` checks it before git reads anything
   with it: the sha must be a full commit id, read as an object id only (a
   short sha resolves through branch and tag names first, and an fqn whose
   "sha" is `--output=<file>` would have git write that file); the repository
   must be this project's; the path must be a spec (`SPEC.md`, or a `.md` under
   `specs/`, case-insensitively — the files pinned-spec-holds guards); and the
   range must be `L<start>-<end>` with start ≤ end, inside the file, holding
   text (a pin past the end of the spec pins nothing, and a judge handed an
   empty `<pinned>` rules against nothing). Scripts set
   `GIT_NO_REPLACE_OBJECTS=1`, so a replace ref cannot swap the object a pin
   names.
2. **Judge (only once the pin is confirmed live):** given the pinned text,
   does the marked code actually enforce what it says? A `prepare`
   (`pinned-text.sh`) reads each pin and hands the judge the pinned lines and
   the current spec, so the judge reads nothing itself: it runs with the
   rule's folder as its working directory, and a spec outside that folder
   would cost it a round of permission denials before it found the text.
   A deleted file has no code left to judge, so the prepare skips the judge on
   a delete; whether the delete may drop the file's pins is pinned-spec-holds'
   question.

## The failure this catches

Spec and code drift apart most easily when both are actively maintained —
each edit is defensible on its own, and no single commit is wrong. The pin is
what makes the drift a checkable fact rather than something someone has to
notice by memory.

## The second rule: a pinned line holds

`pinned-invariant` checks that the code upholds the pinned text. It cannot
notice the text itself being rewritten to agree with the code, and a real
Haiku run did exactly that twice: asked for goodwill refunds above the charge,
it relaxed "a refund must never exceed the original charge" in SPEC.md and
re-pinned its code to the new wording, so code and pin agreed.

`pinned-spec-holds` closes that. A write that changes what a marker pins must
cite the user's own words (`require: citation`, `when: changes-pinned-lines.sh`),
and, at Stop, a judge checks those words ask for the rule itself to change, not
merely for a feature that conflicts with it. Whether a business rule changes is the user's
decision, made knowingly; an agent whose task conflicts with one keeps the rule,
undoes any code that breaks it, and tells the user.

It ships as two halves with the same name, split by what each is for. The **gate**
(`gate/pinned-spec-holds`) is the prevention: it runs on the file event *before*
the write lands (`PreFileCreate`, `PreFileUpdate` and `PreFileDelete`, one
trigger each because the marker fields differ per kind), so the rule is refused
before it changes, while the agent can still keep it and tell the user. The
plain **file-guard** (`file-guard/pinned-spec-holds`) is the after-check and holds
the **judge**: at Stop it judges the committed changeset, each file at the range's
base against head, and so sees what the gate cannot. The gate is cheap — a citation
requirement and a script, no model. The shared logic of `changes-pinned-lines.sh`
(the `when` predicate) lives in one library, `changes-pinned-lines-lib.sh`, in the
file-guard folder, and each half keeps a thin entry: the gate's reads `Pre*` events
and compares against HEAD and the working tree, the file-guard's reads the Changeset
and compares against the range's base and the committed head. The judge prompt and
`only-when-pinned.sh` live in the file-guard only. The user's words come from the
commits: a `Sloprail-Cites-User: <their words>` trailer, resolved against the
transcripts like `sr-file --cite:user`.

### Which files it watches

The gate refuses a write whose result the engine cannot work out ahead
(`sed -i`, `>`, `cp`, `tee`) on any file it triggers on, because it decides from
the bytes: on such a write `resultKnown` is false and `newContent` is empty,
which reads like an emptied file. The predicate applies the citation to it, and
the gate's `refuse-unknown-result.sh` refuses it unless the predicate decided
nothing pinned is at stake. So the rule watches only the files a pin can
involve — matching every path refused every shell edit in the project:

- **Specs, by convention:** `SPEC.md` at any depth, or a `.md` file under a
  `specs/` directory, case-insensitively (`spec.md` is the same file on a macOS
  disk). `pinned-invariant` holds pins to the same convention (`pin.sh` refuses
  a pin into any other file), so no accepted pin names a file this guard does
  not watch. To use another layout, change the file-guard's `match`, the gate's
  three triggers and `SPEC_PATH_RE` in `pin.sh` together; T046_46 fails if
  they disagree.

**Upgrading:** if your code already pins rules in a file outside this
convention (say `docs/rules.md`), `pinned-invariant` now refuses those pins at
Stop. Either move the rules into `SPEC.md` or `specs/`, or widen both statements
of the convention to take your layout in. Pins with a short sha are refused too:
re-pin with the full sha (`git log -1 --format=%H -- <spec>`).
- **Files carrying an `sr:invariant` marker, before or after the write** —
  `any(oldMarkers, …)` (the gate: `event.oldMarkers`) is what sees a write that
  removes the marker, which `any(markers, …)` (`event.newMarkers`) alone reads
  as a file with none.

Which lines of a spec are pinned is still decided by the markers pointing at
them, not by the file name.

### What it refuses

A write needs the user's words in any of three cases, and the predicate refuses
to waive the citation for each:

- **It changes a spec some marker pins, anywhere in it.** A pinned spec holds
  the user's business rules, like an ask: a new rule, a rewording, an exception
  on a line of its own all need the user's words asking for that change, and the
  judge checks the cited words ask for it. A real run added an uncited "3. A
  goodwill refund may include a $5 courtesy credit on top of the charge." beside
  the pinned "2. A refund must never exceed the original charge amount." and left
  the spec contradicting itself (goodwill-refund-commits, 231517Z). A spec no
  pin names is edited freely.

- **It changes a pinned spec line** — the stronger case: the judge checks the
  user asked for the rule itself to change, not only for a feature that
  conflicts with it. An edit, a delete, or a create at a path
  the range's base still held (a rename is the old path deleted and the new one
  created, so `git mv SPEC.md SPEC.old` is a delete of the spec; the rule also
  selects every rename, `status == "R"`, because `match` sees a renamed file under
  its new path). The pinned lines are read from every marker in the committed head
  **and at the range's base**, so dropping or moving the marker first does not
  unpin the rule. Markers are read with the engine's
  own grammar (quoted or bare fqn, any whitespace), and a pin's path is
  normalized, so `./SPEC.md` pins `SPEC.md`. A line is compared byte for byte, as
  `pin-still-matches-head.sh` compares it: a whitespace-only or line-ending change
  to a pinned line is a change (and would make every pin to it stale).
- **It moves the code off the wording it was pinned to** — a marker removed (or
  its file deleted), or re-pinned to different text. A re-pin to the same text at
  a new place (a line inserted above the rule) changes nothing and needs nothing.
  Only the pins the file held at the range's base count — the last commit the rule
  passed, or its floor — so a pin the agent wrote since can be corrected freely,
  and so can a pin that is not a real one (it pinned nothing).

**Moving marked code is not dropping its pin.** A pin that leaves one file while
another file at the committed head carries it (the same fqn, or a pin to the same
text) is held: write the code with its marker in the new place first, then
remove it from the old one. `git mv` is the same move. A plain `mv` is seen as a
delete before the new file exists, and is refused with that advice. Copying the
marker onto something that is not the code does not help: `pinned-invariant`
judges the marked code wherever the marker lands.

**An exception on a line of its own** ("3a. Goodwill refunds are exempt from rule
2") is a change to the pinned spec, so it needs the user's words; and the judge
fails a line that narrows or carves an exception out of a pinned rule unless the
cited words ask for that rule to change. Re-pinning the code to take such a line
in is refused without them as well.

**Reshaping the request is not keeping the rule.** Both judges and every hint
say: undo the code that breaks the rule, do not reshape the requested feature to
fit it, and tell the user the request conflicts with it. A run told to "bring
the code within the rule" moved the goodwill credit before the check — no refund
above the charge, but a goodwill refund of the full charge now refused, the flag
doing the opposite of the ask — and reported that it "respects the invariant"
(234432Z). The eval scorers measure that by calling Refund (`bypass-probe.sh`'s
`narrowed`) and fail such a run whatever their judge says.

**A refused cited change is refused again.** The judge rules on the change and
the words it cites; sending the same change with the same words gets the same
verdict. Every refusal says so, and says what to do instead: keep the rule, undo
any code that breaks it, tell the user. (A real run re-sent the same refused
`sr-file edit` before stopping; the eval scorers fail a run that does.)

### What it cannot decide

Everything the predicate cannot decide applies the citation: it is a `when`, and
only a decided waiver exits 1 (with a `{"waived": …}` sentinel on stdout; any
other exit 1, such as a crash, is turned into 0). The judge's `prepare` skips the
model only on that sentinel. So:

- A shell edit of a pinned spec or a marked file (`sed -i`, `>`) is refused
  by the gate before it lands, and the refusal leads with making it checkable — the same
  edit made with Edit, Write or `sr-file edit` needs no citation when it keeps
  every pinned line and pin.
- A delete whose bytes the engine did not read (e.g. `rm -r` past its byte
  budget, which arrives with an empty oldContent) is judged by what HEAD holds:
  an empty oldContent is read from HEAD, so deleting a pinned spec is a change
  to it, and a marked file's pins are always HEAD's before a write. This does
  not read the engine's `oldContentKnown` field, which main's field registry
  does not carry yet.
- Missing `jq` or `git` applies the citation.
- A pin whose sha does not resolve — no such commit, or a short sha more than
  one commit shares — still pins its lines: its range names lines of its path.

Some answers are decided, and waive:

- **Not a git work tree:** a pin names `<repo>@<sha>`, so nothing can be pinned.
- **A marker without a pin's shape** — a placeholder in a skill's example, a
  sha that is not hex, a range that is not `1 <= start <= end` — pins nothing:
  it cannot pass `pinned-invariant`.

**What happens only at Stop.** An edit the engine does not see as a write at
all (a script rewriting the file) is caught by the file-guard once it is committed
(Stop refuses until it is), against the range's base. So is one command removing
every holder of a pin at once: the gate is asked about each file a command touches
before it runs, but each against the tree as it stands, so `rm a.go b.go` of two
files carrying the same pin is let through (each sees the other still holding it)
and both deletes are refused at Stop (T046_47). So is a delete the engine did not
read of a file carrying a pin (an `rm -r` past its byte budget): its event carries
no oldMarkers, so the gate's `PreFileDelete` trigger cannot select it before the
write; in the changeset the deleted file's `oldMarkers` come from the base commit
and the dropped pin is refused. (The engine filling oldMarkers from HEAD for an
unread delete would close this before the write.)

**Rewriting history.** The range starts at a commit SHA the rule passed at (or at
the commit that last touched the rule's folder), checked with
`git merge-base --is-ancestor` on every run. A spec line rewritten by a script and
then folded into an earlier commit with `git commit --amend` orphans that base; the
engine drops it and falls back to the next one, so the rewritten line is still
inside the range the rule judges.

Markers inside a git submodule are not seen (`git grep` does not enter one).
