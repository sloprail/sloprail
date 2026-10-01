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

A file-guard's match sees a file's own facts **bare** — `path`, `status`,
`markers`, `oldMarkers`, `trailers`, `context`, not `event.path`
([matchers.md](matchers.md)).

## When it fires: at Stop, over the commits since it last passed

A file-guard is evaluated **once per rule at Stop**, over its range of commits:
from its base to `HEAD`, as one squashed net diff (`git diff -M base head`). The
base is, in order:
**the rule's watermark** — the latest head the rule passed, *at any definition of
the rule*, if it is still an ancestor of `HEAD` (work up to it was approved, even
under an older rule, and is not judged again; editing the rule changes its hash,
so verdicts are not replayed, but the watermark stays); otherwise **the floor**: the
parent of the last commit that touched the rule's whole `.sloprail` root (a rule
in this repository: the commit that adds or changes a rule, a schema or a shared
script is judged by the rule; a root commit has no parent, so its base is git's
empty tree). A rule that did **not exist at session start**
(its folder is absent from the session-start commit's tree) applies **from the
commit that first added its folder**: the parent of that commit (never later than the floor, so a later touch of `.sloprail` cannot hide what followed the add), earlier history grandfathered, so adding a
rule mid-session does not judge the whole session. A rule that **existed at session
start** (including one deleted and re-added in the session) takes **the HEAD recorded
when the session began**, never later and earlier only as below: a rule last changed
long before the session does not re-judge every commit merged since, and a violating
commit followed by a commit under `.sloprail` is still judged. The base is **extended
backwards over any range an earlier session of the same worktree was refused for and
never fixed** (read, read-only, from that worktree's other sessions' check stores: a
refusal with no later pass at a descendant of its head, whose head is still an ancestor
of `HEAD`), so a refusal does not vanish when a second session starts; what passed or
was never checked stays grandfathered, and a store that cannot be read fails the range. When the session start is
unknown or unborn the rule counts as existing (unborn: the whole history). For a
plugin's rule, whose `.sloprail` root is in the plugin cache, the base is the session
start. Every base is a SHA, checked with
`git merge-base --is-ancestor` on every run, so an amend, rebase or branch switch
drops a base that no longer exists instead of silently shrinking the diff. The session
start is the HEAD the session FIRST began at, kept even when the tree later leaves its
history (a session that began before the first commit starts at git's empty tree). Any
anchor the tree left (an amend, rebase or reset rewrote it) is re-anchored at its
merge base with `HEAD` (a dropped watermark overrides an older surviving pass; a
session start git no longer has, or that shares no history with `HEAD`, falls to the
empty tree, so everything is judged, the root commit's own content included). If none
is usable (no watermark, no committed rule, no session start recorded, or a session that
began before the start was kept) the
evaluation **fails** and Stop refuses, rather than guess; a range where `match`
selects nothing is a pass with no `files`, never the same as a range that could
not be computed.

**Every branch the session committed on is judged, not only `HEAD`.** The commits a
session (or sub-agent) makes are recorded per folder as data (the `session_refs` table of
the session's store: folder, ref, first and latest tip). At each Stop the engine reads the
folder's `HEAD` reflog (a worktree has its own) for the commits made since the session
began, on any branch or on a detached `HEAD`, and records every line of history still
reachable from a ref (or left detached). The rule is then evaluated once for `HEAD` and
once for each other recorded tip, each with the same range logic (a watermark reachable
from *that tip*, else the floor or session start) and its own `SR_TREE` snapshot. A tip that
`HEAD` or another tip contains is dropped, and a pass is a watermark for every tip that
contains it, so a commit is judged once per rule. A refusal for a tip that is not checked
out names the branch and folder (`git -C <folder> switch <branch>`, fix, commit); detached
commits are named `detached/<sha>` and need a branch (`git switch -c <name> <sha>`). A
branch that existed before the session and was not committed on is never judged. A ref
the engine did not see can be recorded by hand:
`sr-session refs add --session <id> --workspace <dir> --folder <git root> --ref <branch> --tip <sha>`
(`sr-session refs list` shows the rows; without `--agent` the row belongs to the root session,
with it to that sub-agent). A tip starts at its ref's creation point (the oldest reflog entry),
so upstream commits merged before a branch was cut are not blamed on the agent, and a tip
already inside a branch's history when the session began is not the session's work. The refs are recorded from a snapshot taken at every hook (each branch checked out in the
folder, and a detached `HEAD`), the reflog being only a backfill. A repository the agent
runs history-moving git commands in outside its own tree (`git -C <dir> commit`,
`cd <dir> && git commit`, merge, rebase, pull, push, `git worktree add <dir>`) is registered
as an ad-hoc folder before the command runs, started at its `HEAD` then, and judged at Stop
under THAT repository's own `.sloprail` plus the session's plugins. `git push` and
`gh pr create` can be gated too by the plugin's `judge-before-push` gate, which ships **off**
and is turned on with `enabled: [sloprail/gate/judge-before-push]` in `.sloprail/config.yaml`
([gate.md](gate.md)): before the command runs it calls `sr-session judge` (every file-guard
over HEAD and each recorded ref of the target repository, as Stop would) and a refusal blocks
the command.
A branch the user genuinely dropped is abandoned with
`sr-session refs abandon --ref <branch> --cite-user '<exact quote>'`: the quote must resolve
to a USER message of the session (never an assistant's or a tool's). It is abandoned at its
current tip, and judged again if the tip moves or the commit is pushed or merged; deleting
a branch is not an abandon, its recorded tip is still judged.
Uncommitted work is a `commit-required` matter and
stays `HEAD`/worktree-only. Sub-agents judge their own folders' tips at their own Stop.

**A tip stays owed until a rule passes it, however the branch goes away.** Every recorded tip
is pinned under `refs/sloprail/pins/` (a hidden ref, outside `refs/heads`, `refs/remotes`
and `refs/tags`), so deleting its branch, removing its worktree or running
`git gc --prune=now` does not lose the commits; the pin is dropped once every rule has passed
the tip. Where a ref was cut is remembered the first time it is seen, because a deleted branch
loses its reflog. A ref moved off a recorded tip by something that is not a fast-forward
(`branch -f`, `checkout -B`, `update-ref`, `reset`) keeps that tip: a branch that holds it later
(a copy made before, one recreated from the reflog) is recorded and judged. A rule that was
added to the project after an older branch was cut (so its folder is absent from that
branch's history) judges that branch's commits made after the rule was added, not the ones
before. A store written by an older engine is migrated by the first hook that opens it.

**Work whose folder is gone passes to the root.** Each agent judges its own folders, so when
a sub-agent's worktree is removed (the harness fires `WorktreeRemove`, which the plugin's hook
`sr-session worktree-remove` answers, never blocking the removal, by pinning what the folder
held and marking it removed; or the path simply no longer exists), or rows are recorded under a
folder that is not a folder of the session at all, the **root agent's next Stop judges them**:
each tip on its own tree, in the repository it belongs to, with that repository's rules. A
live sub-agent's folder is never claimed. The refusal opens with `You now own this:
sub-agent <id> in <path> is gone and its work at <sha> was never judged`. For work that already
landed on the default branch (its branch deleted or not) it says which commit carries it, that
the place to fix it is a **new branch from the default branch** (the exact `git worktree add
<path> -b fix/<name> origin/<default>` command), and that a finding that is only a missing
citation is for the user to decide NOW with `AskUserQuestion` ("keep it" / "revert it"), never
parked; once the default branch no longer holds the refused content the work is settled as
`superseded`. For work on a branch that is gone and did not land it names the command that
restores the branch from the pinned tip.

**One check-results database serves the whole session family** (the root's `checks.db`,
written by the root and every sub-agent; each run carries its `agent_id`), so a sub-agent's
pass on a commit is the root's too and a sub-agent's refusal is visible to the root's merge
gate. The per-agent `state.db` stays per agent. A sub-agent's own database from an older
engine is imported into the family's once, and left in place.

A rule's identity is its whole `.sloprail` root — its own folder, every other rule,
schemas and shared scripts, whichever of the project's or its plugin's it lives in.
The rule hash covers all of it, so editing any file there changes the hash and
invalidates stored verdicts (the watermark stays). A file-guard must therefore not
write into `.sloprail` (ledgers, caches): a write there changes the hash each time. Keep such state in
`sr-session state` or under `.git/`.

A rename is selected if `match` holds on its new path **or** on the path it came
from (with the markers it carried there): moving a file out of a guarded path,
`git mv memories/x.md archive/x.md`, is a change to it. The same goes for an
uncommitted rename under commit required. `deletions:` does not change this — a
rename is not a deletion.

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

## What a check receives

**Requirements are per subject, checks are per changeset.** `require` (and each
entry's `when`) is evaluated once per **subject**, and by default a subject is one
selected file: the payload's `subject` is `{id: "<path>", files: ["<path>"]}` and the
whole changeset stays in it as context. So a `when` decides for `.subject.files` (one
file), and the requirement applies only to the files whose `when` applies: a
citation is asked of the file whose own change removes content, not of the file
beside it that only adds. A refusal names every subject it failed for. `checks` default
to ONE subject, the whole changeset (`subject.id` `"changeset"`, `files` every selected
file), so a script loops over `.changeset.files[]` and a judge sees the whole change.
A `subjects:` key, when it lands, will supply the subject list for both without
reshaping the payload. The check results store each requirement row under its subject.

A `Changeset` payload, shaped in [events.md](events.md#changeset--what-a-file-guards-checks-receive).
A script loops over `.changeset.files[]` (a rule about one file at a time — size,
frontmatter — is that loop), reading whatever else it needs from `SR_TREE`
([environment.md](environment.md)); a judge renders `{{ changeset }}` and
`{{ change }}` ([judge-checks.md](judge-checks.md)).

## Seeing what a rule will be handed

```bash
sr-session changeset --rule size-limit
```

prints JSON and **runs nothing**: no check, no judge, no verdict recorded, no
watermark moved. Use it to write a rule's `match`, script and rubric against real
input, and to find out why a rule selected (or missed) a file. `--rule` is the
folder name (`size-limit`) or the qualified name a refusal cites
(`file-guard/size-limit`, `<plugin>/file-guard/size-limit`). Its keys: `rule`;
`origin` (which base was used: `watermark`, `floor` or `session-start`); `base`,
`head`; `droppedWatermark` (a watermark no longer reachable after an amend or
rebase, when there was one); `ruleHash` (a hash of the rule's whole `.sloprail` root — edit
anything in it and old verdicts stop applying); `unresolvedCitations` (the
`Sloprail-Cites-*` trailers whose quote did not resolve); and `payload`, exactly
what a check receives on stdin.

## Preventing a write is a gate, not a file-guard

There is no `preventive:` key. A file-guard declaration still carrying one (with
any value) is **refused at load**, with a message telling you to split it. To
refuse a write before it lands — the useful moment for a rule you would rather
enforce *before* the loss than report *after* it — write a **gate** bound to the
pre-write event, and keep a plain file-guard for the committed result:

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
in the file-guard, which rules on the committed result at Stop.
A script both halves need is written once, as a library that keeps no event-kind
logic (in the file-guard folder, `<script>-lib.sh`); each half keeps a thin entry
that reads its own input — the `Pre*` event in the gate, the `Changeset` in the
file-guard — and sources it (`. "$lib_dir/<script>-lib.sh"`; the gate's entry finds it at
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
transition), use a script check that reads `.changeset.citations` (on a gate, `.event.citations`). Either way the
agent makes the change with `sr-file ... --cite:user '<quote>'`, and Write, Edit,
`sed` and `rm` are refused. Put the requirement on a `PreFileWrite` gate, so the
ungrounded write is refused before it lands, and keep a plain file-guard beside
it for the committed result. The full pattern is in [grounding.md](grounding.md).

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

With `include` or `only`, a deleted file is a `D` entry in `.changeset.files[]`.
A guard on the default never gets one — do not write a script branch to wave
deletes through, leave the key off. To refuse a delete **before**
it happens, bind a gate to `PreFileDelete` (below).

On a `D` entry, a check reads what was lost: `oldContent` and `oldMarkers`. The
guard's own `match` sees the deleted file's markers too — for a delete, the
scope's `markers` is the file's `oldMarkers` — so a marker-scoped guard
(`any(markers, .kind == "invariant")`) that includes deletions still selects the
file it is about. On a `PreFileDelete` (a gate), read `oldContentKnown` before
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
committed changeset, or state the rule keeps itself):

- **Files:** past 1000 files the directory predicts **nothing** — the command
  runs, and a file-guard with `deletions: include` sees the files tracked in its
  range as `D` entries once the deletion is committed (a file created and
  removed without ever being committed leaves no difference at all).
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
  script, a program named by a variable — predicts nothing; only the committed
  changeset sees it.

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

## Passed ranges and replayed fails

Every evaluation is recorded (`sr-checks status`, `sr-checks sql`), and three
rules follow from it:

- **A passed range is never re-delivered.** When every check passes, the rule's
  watermark moves to that head; the next Stop judges only commits made after it. A
  Stop with no new commits runs no check.
- **Unchanged input is never re-judged.** A judge's verdict is stored under a
  fingerprint of the rule's whole `.sloprail` root, the model and everything the judge was
  given (never commit SHAs, so a rebase that changes SHAs but not content is a hit),
  and replayed — a **fail included** — until the input changes. A script is cheap
  and deterministic and always re-runs. Editing anything under `.sloprail`, or
  changing its `model`, starts the verdicts over.
- **Changed input re-judges the whole squashed range, on purpose.** A range that
  was refused does not advance, so a fix commit is judged together with the
  commits it fixes. A stored failure whose input has left the range is cleared as
  stale, not left standing.

**Stop order.** Context enters → commit-required → file-guards → gates → context
exits. A `match` or `when` that reads `context["<name>"].active` therefore sees a
context entered in this very turn, and one that exits at this Stop is still active
for it (see [context.md](context.md#the-stop-order)).

**Rules run concurrently, scripts before judges.** The file-guards of one Stop are
evaluated at the same time, on a pool of 6 (set `SLOPRAIL_STOP_CONCURRENCY` to
change it; `1` is one rule at a time). Within a rule the checks still run in the
order you declared them and the first refusal ends the rule. Across rules, every
rule's `require` entries and script checks that precede its first judge run before
any of its judges starts. A refusal from one of those defers only THAT rule's own
judges: each is recorded as a `skip` row with the reason (`sr-checks status` shows it)
and runs in a later Stop once the cheap check passes. Every other rule's judges still
run in the same Stop, so their refusals are reported beside the cheap one. A Stop
reports each rule's time on stderr (`cheap checks`, `judges`, and the total).

A file-guard has no `seen`: it is handed a changeset, not Post events. `seen`
remains on the Post events a **context** binds.

## Reading the change

A file-guard reads the committed change from `changeset.files[]`
([events.md](events.md#changeset--what-a-file-guards-checks-receive)); a deleted
file is in `files` only under `deletions: include` or `only` (above), and under
the default `skip` is listed in `others`.

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

Markers (`// sr:<kind>`) are a **list** of `{kind, fqn, line}`. A file-guard's
script reads them per file, `.changeset.files[].newMarkers` (at `head`) and
`.oldMarkers` (at the range's base); its `match` reads the same two sets bare, as
`markers` and `oldMarkers` (on a delete, `markers` are the ones the deleted file
carried). A gate on `PreFileWrite` reads the event's `event.newMarkers` /
`event.oldMarkers`, per trigger kind (`PreFileCreate` has no `oldMarkers`). The
per-kind field set and the element shape are in [events.md](events.md); read them
with a quantifier:

```
any(newMarkers, .kind == "decision")     would the result carry one
len(newMarkers) == 0                      does the result carry none
any(oldMarkers, .kind == "asked")         did the file already carry one
```

A marker's quote is on `.fqn`:

```bash
quote="$(printf '%s' "$payload" \
  | jq -r '.changeset.files[0].newMarkers[] | select(.kind == "asked") | .fqn' | head -1)"
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
