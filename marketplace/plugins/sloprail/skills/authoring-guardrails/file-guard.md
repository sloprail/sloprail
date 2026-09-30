# File-guard

A file-guard judges a **changeset**: the net change between two commits — what
the agent committed since the rule last passed. Its whole job is to answer "is
this change OK?". It judges **commits**, never the working tree, so a half-finished
edit is never judged; it is post-factum only, and it keeps refusing (replaying its
verdict) until the change is fixed. It never acts before a write lands — refusing
a write or a delete *before* it happens is a **gate's** job (below).

```
.sloprail/file-guard/<name>/file-guard.yaml
```

```yaml
# preserves-unasked-content — an edit must not silently drop content nobody
# asked to remove. (Its gate of the same name refuses the loss before it lands.)
match: 'path startsWith "memories/" and path endsWith ".md"'
deletions: include
require:
  - citation: {source_types: [user]}
    when: ./removes-content.sh
checks:
  - judge: ./change-is-clean-and-absolute.md.j2
    prepare: ./skip-pure-addition.sh
```

`match` narrows to the files this rule is about (a glob or an expression —
[matchers.md](matchers.md)). `require` lists what must hold before any check
runs — here a citation of the user's words, `when` the change removes something
([grounding.md](grounding.md)). `checks` is the list of checks, run in order, first
refusal ending it — each a script ([script-checks.md](script-checks.md)) or a
judge ([judge-checks.md](judge-checks.md)). `deletions` is the one
nature-specific knob, below; it is optional.

A file-guard's match sees a file's own facts **bare**: `path`, `status` (`A`, `M`,
`D` or `R` in the changeset), `markers`, `oldMarkers`, `trailers` and `context` —
not `event.path`. `markers` is the set of markers the file carries at the end of
the range (a deleted file's are the ones it carried); test them with a quantifier,
`any(markers, .kind == "invariant")`. `oldMarkers` is the set it carried before
the range, for a rule that must also see a marker removed:
`any(markers, .kind == "invariant") or any(oldMarkers, .kind == "invariant")`.
`trailers` maps each commit-message trailer key to its values across the range's
commits: `"move-only" in (trailers["Sloprail-Refactor"] ?? [])`.

## When it fires: at Stop, over the commits since it last passed

A file-guard is evaluated **once per rule at Stop**, over its range of commits:
from its base to `HEAD`, as one squashed net diff (`git diff -M base head`). The
base is the first of these that exists and is still an ancestor of `HEAD`:
**the rule's watermark** (the last head it passed, at its current definition),
**the last commit that touched the rule's folder** (a rule in this repository),
**the HEAD recorded when the session began** (a plugin's rule, or one not
committed yet). Every base is a SHA, checked with `git merge-base --is-ancestor`
on every run, so an amend, rebase or branch switch drops a base that no longer
exists instead of silently shrinking the diff. `sr-session changeset --rule
<name>` prints the range and the payload without running anything
([changeset.md](changeset.md)).

A file-guard must not write into its own rule folder (ledgers, caches): the rule hash covers the whole folder, so any write changes it, voids the watermark, and re-judges forever. Keep such state in `sr-session state` or under `.git/`.

**Commit required.** Work that is not committed cannot be judged, so at Stop an
uncommitted change to a path some file-guard's `match` selects refuses the Stop:
"commit these". It is always on, never commits for the agent, applies only to an
agent that owns the tree (a sub-agent working in the session's own tree is not
gated, one in a worktree of its own is), and lets go after `stop_hook_block_cap`
refusals of the same uncommitted set. Put the user's words in the commit message
as a trailer where the change is grounded (below).

The change is already committed, so refusing does not undo it; it tells the agent
the cycle is not finished and it must fix what it did — by committing a fix, which
is judged together with the commits it fixes. The net result is what is judged:
if a later commit fully restores what an earlier one removed, the range passes.
That makes a file-guard right for a rule about the **result** of a piece of work
("every new file under `memories/` has frontmatter"). It is never handed a `Pre*`
event, and its checks read the commits, never the working tree.

## What a check receives: the Changeset payload

A script gets the payload on stdin; a judge's template renders it. `event` is
always `{"kind": "Changeset"}`:

```json
{"event": {"kind": "Changeset"},
 "changeset": {
   "base": "…", "head": "…",
   "commits": [{"sha": "…", "subject": "…", "body": "…",
                "trailers": {"Sloprail-Cites-User": ["…"]}}],
   "files": [{"path": "…", "status": "M", "oldPath": "", "oldContent": "…",
              "newContent": "…", "oldMarkers": [], "newMarkers": [], "diff": "…"}],
   "others": [{"path": "README.md", "status": "M"}],
   "citations": [{"quote": "…", "sourceTypes": ["user"], "path": "…", "line": 3, "message": "…"}]},
 "subject": {"id": "changeset", "files": ["…"]},
 "transcriptPath": "…", "context": {}}
```

`files` is what `match` selected, in full; `others` is the rest of the range as
names only. A script loops over `.changeset.files[]` (a rule about one file at a
time — size, frontmatter — is that loop). It also gets `SR_TREE`, a **read-only
snapshot of `head`** to read whatever else it needs (a sibling spec, a test file)
as committed, and `SR_BASE` and `SR_HEAD`. A judge's template renders
`{{ changeset }}`, `{{ subject }}`, and `{{ change }}` — the combined diff of the
selected files.

Do not write into your own rule folder from a check (a ledger, a cache): the rule's
hash covers the whole folder, and a changed hash starts the rule again from its floor.

## Preventing a write is a gate, not a file-guard

There is no `preventive:` key. A file-guard declaration still carrying one (with
any value) is **refused at load**, with a message telling you to split it. To
refuse a write before it lands — the useful moment for a rule you would rather
enforce *before* the loss than report *after* it — write a **gate** bound to the
pre-write event, and keep a plain file-guard for the settled result:

```yaml
# .sloprail/gate/preserves-unasked-content/gate.yaml — the prevention
on:
  - event: PreFileWrite
    match: 'event.path startsWith "memories/" and event.path endsWith ".md"'
  - event: PreFileDelete            # only when losing the file is the rule's business
    match: 'event.path startsWith "memories/"'
require:
  - citation: {source_types: [user]}
    when: ./removes-content.sh
checks:
  - script: ./require-known-result.sh   # refuse a write whose result is unknown
```

```yaml
# .sloprail/file-guard/preserves-unasked-content/file-guard.yaml — the result
match: 'path startsWith "memories/" and path endsWith ".md"'
deletions: include
checks:
  - judge: ./change-is-clean-and-absolute.md.j2
```

The gate gets everything a create or an update carries — `newContent`,
`resultKnown`, `newMarkers`, `citations` — and `require: citation` works on it
exactly as on a file-guard ([gate.md](gate.md), [grounding.md](grounding.md)). A
`PreFileDelete` gate reads `oldContent`, `oldContentKnown` and `oldMarkers`, the
bytes about to be lost. A call that changes several files (`rm a.go b.go`, two
`sr-file` calls joined by `&&`) wakes the gate **once per file**, and the one
refusal names every file it refused.

Keep what the gate decides small and cheap (a `require`, a script); keep the judge
in the file-guard, which rules on the settled result at Stop.
A script both halves need is written once, as a library that keeps no event-kind
logic (in the file-guard folder, `<script>-lib.sh`); each half keeps a thin entry
that reads its own kind — `Pre*` in the gate, `Post*` in the file-guard — and sources
it (`. "$lib_dir/<script>-lib.sh"`; the gate's entry finds it at
`../../file-guard/<rule>/`).

**A gate does not fail closed on an unknown result by itself — the engine adds nothing.** A command, `sed`
or `git` edit whose result the engine cannot derive reaches the gate with
`resultKnown: false` and an empty `newContent`. A gate whose decision reads the
content must refuse it (see [The resultKnown discipline](#the-resultknown-discipline)),
or the write slips through to be judged only at Stop.

## Grounded changes

A file whose changes must trace to something the user said, such as a goal, a
rule or an ask, requires a **citation** on the change instead of a transcript
quote stored in the file. When every change must be grounded, use
`require: [{citation: {source_types: [user]}}]`. When only some must be (a removal, a status
transition), use a script check that reads `.event.citations`. Either way the
agent makes the change with `sr-file ... --cite:user '<quote>'`, and Write, Edit,
`sed` and `rm` are refused. Put the requirement on a `PreFileWrite` gate, so the
ungrounded write is refused before it lands, and keep a plain file-guard beside
it for the settled result. The full pattern is in [grounding.md](grounding.md).

## Deleted files: `deletions`

A deleted file has no end state — no `newContent`, no `newMarkers` — so most
guards have nothing to judge once it is gone. `deletions` says whether a delete
is this guard's business:

| `deletions:` | the guard runs on | use it for |
|---|---|---|
| `skip` (**default**, also when absent) | creates and updates | a rule about what a file **holds** — frontmatter, citations, a rubric. A deleted file holds nothing. |
| `include` | creates, updates **and** deletes | a rule that also covers **losing** the file — "no content under `memories/` is removed unasked", "an invariant-pinned file may not quietly disappear". |
| `only` | deletes only | a rule that exists purely to catch a file **going away**. |

```yaml
deletions: include
```

A file-guard that includes deletions is asked on the `PostFileDelete` at Stop. A
guard on the default never sees one — do not write a script branch to wave deletes
through, leave the key off. To refuse a delete **before** it happens, bind a
gate to `PreFileDelete` (below).

On a delete, a check reads what was lost: `oldContent` and `oldMarkers`. The
guard's own `match` sees the deleted file's markers too — for a delete, the
scope's `markers` is the file's `oldMarkers` — so a marker-scoped guard
(`any(markers, .kind == "invariant")`) that includes deletions still selects the
file it is about. On a `PreFileDelete`, read `oldContentKnown` before
`oldContent`: it is `false`, with `oldContent` `""`, when the bytes were not
read (below) — "the file was empty" and "the engine did not look" are otherwise
the same string.

**Which shell commands reach a `PreFileDelete`.** `rm <file>`, `mv <file> …`
and `git rm <file>` name the file directly. A recursive removal of a DIRECTORY —
`rm -r`/`-R`/`--recursive` (or an abbreviation, `--rec`), `git rm -r`, or `mv`
of the directory — is expanded into one `PreFileDelete` per file inside it, so a
guard on `scanners/x/scanner.yaml` fires on `rm -rf scanners/x`. The expansion
has limits, and a `PreFileDelete` gate that must hold past them needs the
file-guard beside it as the backstop that does not depend on the prediction (the
Post-phase tree diff, or state the rule keeps itself):

- **Files:** past 1000 files the directory predicts **nothing** — the command
  runs, and only files in the session's baseline surface afterwards as
  `PostFileDelete` at Stop (a file created and removed in the same session
  leaves no difference at all).
- **Bytes:** at most 8 MiB is read across the directory. Every file is still
  predicted; one that does not fit in what is left of that budget is not read
  (`oldContentKnown: false`) and charges nothing, so smaller files after it are
  still read. The same for one file over 8 MiB, and for a file that is not a
  regular file once links are followed (a FIFO or a device is never opened for
  reading). `sr-file delete` reads the same way. An unread file's `oldMarkers`
  come from its copy at HEAD (tracked, within the cap), so a guard whose
  `match` reads markers still selects it before the delete lands.
- **Unreadable paths:** a subdirectory the walk cannot read is skipped and
  reported on the hook's stderr; the files around it are still predicted.
- **Unseen commands:** a delete the parser does not model — `find … -delete`, a
  script, a program named by a variable — predicts nothing; only the tree diff
  sees it.

One key with three values, not a list of events: a file-guard binds to a file's
state, and "is a file that no longer exists my business" is the one place that
question forks. Anything other than the three values is refused when the rule
loads (`sr-file declarations .sloprail` reports it).

### Refusing a delete before it happens

A gate on `PreFileDelete` sees the bytes about to be lost and refuses the delete
before it runs:

```yaml
# .sloprail/gate/no-silent-removal/gate.yaml
on:
  - event: PreFileDelete
    match: 'event.path startsWith "memories/"'
checks:
  - script: ./refuse-unless-asked.sh   # reads .event.oldContent, .event.oldContentKnown
```

`rm a b` wakes the gate once per file, so a call is refused if any of its files
fails, and none is deleted. The gate's `match` reads the kind's own fields
(`event.path`, `event.oldMarkers`); a marker-scoped delete rule is
`any(event.oldMarkers, .kind == "invariant")` on the `PreFileDelete` trigger.

## What replaces re-firing and `seen`

Every evaluation is recorded (`sr-checks status`, `sr-checks sql`), and three
rules follow from it:

- **A passed range is never re-delivered.** When every check passes, the rule's
  watermark moves to that head; the next Stop judges only commits made after it. A
  Stop with no new commits runs no check.
- **Unchanged input is never re-judged.** A judge's verdict is stored under a
  fingerprint of the rule's whole folder, the model and everything the judge was
  given (never commit SHAs, so a rebase that changes SHAs but not content is a hit),
  and replayed — a **fail included** — until the input changes. A script is cheap
  and deterministic and always re-runs. Editing anything in the rule's folder, or
  changing its `model`, starts the verdicts over.
- **Changed input re-judges the whole squashed range, on purpose.** A range that
  was refused does not advance, so a fix commit is judged together with the
  commits it fixes. A stored failure whose input has left the range is cleared as
  stale, not left standing.

`seen` no longer exists for a file-guard: there is no per-file Post event to
re-send. It remains on the Post events a **context** binds.

## Reading the change

A file-guard reads the committed change from `changeset.files[]`. A **created**
file has `status` `A` and its body in `newContent`; a **modified** one has `M`, the
range's first bytes in `oldContent`, the last in `newContent`; a **renamed** one
has `R` and `oldPath` (a rename is not a deletion); a **deleted** one has `D`, its
`oldContent` and `oldMarkers`, and no `newContent` — and reaches the rule only when
`deletions:` is `include` or `only` (above), in which case it is in `files`; under
the default `skip` it is listed in `others`. `diff` is that file's part of the
squashed diff.

Read the file from `SR_TREE` (`cat "$SR_TREE/$path"`), never from
`$SR_WORKSPACE`: the working tree may hold work that was never committed.

### The resultKnown discipline

This is the trap that makes a **pre-write gate** silently permissive. On a
`PreFileCreate` or `PreFileUpdate` an **absent `newContent` reads as the empty
string**, which is indistinguishable from a write that empties the file. The
engine could not work the result out — a `sed -i`, a `git apply`, a notebook
create whose cell source is not the document, an `sr-file` line it could not
resolve — and says so with **`resultKnown: false`**.

**Guard on `resultKnown` before you read `newContent`**, and decide what an
unknown result means for your rule. A gate that exists to *prevent* must fail
closed:

```bash
if [ "$(printf '%s' "$event" | jq -r '.event.resultKnown // false')" != "true" ]; then
  echo '{"reason":"the result of this write could not be computed (an in-place or environment-dependent edit), so it cannot be checked before it lands. Write the file content directly."}'
  exit 1
fi
```

In a gate's trigger `match` (which reads the event under `event`),
`event.resultKnown and not (event.newContent contains "---")` selects a strip and
says nothing where the engine cannot see — the `event.resultKnown and` short-circuits
false — and `not event.resultKnown` selects the underivable cases deliberately. A create from an ordinary Write always carries
`newContent`, but `PreFileCreate` carries `resultKnown` too (a notebook create can
be false), so check it on both kinds.

A file-guard needs none of this: it reads committed bytes (above), never a
prediction.

## Markers

A file event carries the `// sr:<kind>` markers as a **list** of `{kind, fqn,
line}` — `newMarkers` (the result's markers) on the create and update kinds,
`oldMarkers` (the file's current markers) on the update and delete kinds. A gate
on `PreFileWrite` reads them as `event.newMarkers` / `event.oldMarkers`, per
trigger kind (`PreFileCreate` has no `oldMarkers`). The
per-kind field set and the element shape are in [events.md](events.md); read them
in a check with a quantifier:

```
any(newMarkers, .kind == "decision")     would the result carry one
len(newMarkers) == 0                      does the result carry none
any(oldMarkers, .kind == "asked")         does the file already carry one
```

Note the distinction from a **file-guard's own match scope**, which exposes the
settled file's markers as `markers` (`any(markers, .kind == "invariant")`) — on a
delete, the markers the deleted file carried — and the markers it carried before
the change as `oldMarkers` (empty on a create; the session baseline's at Stop).
`newMarkers`/`oldMarkers` are also the **event's** fields — what a `Pre`/`Post`
file event carries, read by a check off `.event.newMarkers`. In a
script, a marker's quote is on `.fqn`:

```bash
quote="$(printf '%s' "$input" \
  | jq -r '(.event.newMarkers // [])[] | select(.kind == "asked") | .fqn' | head -1)"
```

Write markers with `sr-mark`; see its `--help`.

## Turning one off

Keep the folder and set the guard inert. A file-guard has no per-rule enable
flag in the way the old format did — disable it from `.sloprail/config.yaml` by
its qualified name, which is also how you disable a plugin's:

```yaml
disabled:
  - <plugin-or-project>/file-guard/<name>
```

The sibling prose (a `RUBRIC.md`, a `README.md`, comments in the YAML) holds the
reasoning that produced the rule — keep it, so the next person deciding whether
to switch it back on has the argument in front of them.
