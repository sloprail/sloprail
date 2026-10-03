# File-guard

A file-guard judges a **changeset**: the net change over a range of commits,
`merge-base(base, head)..head`. Its whole job is to answer "is this change OK?".
It judges **commits**, never the working tree, so a half-finished edit is never
judged. It is post-factum only: it never acts before a write lands — refusing a
write or a delete *before* it happens is a **gate's** job (below).

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
judge ([judge-checks.md](judge-checks.md)). `deletions` and `subjects` are the
nature-specific knobs, below; both are optional.

A file-guard's match sees a file's own facts **bare** — `path`, `status`,
`markers`, `oldMarkers`, `trailers`, not `event.path`
([matchers.md](matchers.md)). It cannot read `context`: a file-guard sees no session,
so a condition on a context belongs on a gate.

## When it fires: `sr-checks run` over an explicit range

A file-guard judges **an explicit range of commits**, stated by whoever runs it:

```bash
sr-checks run    --base origin/main --head HEAD   # judges; asks a model only where no pass is stored; stores the verdicts
sr-checks verify --base origin/main --head HEAD   # deterministic: asks no model, writes nothing; exit 1 = red
sr-checks show   --base origin/main --head HEAD   # each subject's latest stored result; always exit 0
```

`--base` and `--head` are both
required (a branch, a tag or a sha). The range is `merge-base(base, head)..head` as one
squashed net diff, so a base that is behind only widens it. The commands know nothing of
sessions or branches: the same content in the range is the same input however it got
there (a rebase, a squash, a revert and re-apply). A range where `match` selects nothing
is a pass with no `files`, never the same as a range that could not be computed.

Anything that goes wrong in the engine (git, the rule's folder, a store that cannot be
read) fails the run closed.

A rule's identity is the files git tracks under its `.sloprail` root (its own folder, the other
rules, shared scripts such as `_lib`), as they are on disk, so an uncommitted edit to any of
them changes the hash and invalidates stored verdicts. Untracked and ignored files (a ledger or
cache a check writes) do not count, so a check may keep such state there; a new, uncommitted rule
hashes its unignored files; a plugin's rule hashes its plugin's `.sloprail` root. Anything git cannot
answer (an error, a folder outside the repository) fails closed.

A rename is selected if `match` holds on its new path **or** on the path it came
from (with the markers it carried there): moving a file out of a guarded path,
`git mv memories/x.md archive/x.md`, is a change to it. The same goes for an
uncommitted rename under commit required. `deletions:` does not change this — a
rename is not a deletion.

The change is already committed, so refusing does not undo it; it tells the agent
it must fix what it did — by committing a fix, which is judged together with the
commits it fixes. The net result is what is judged: if a later commit fully restores
what an earlier one removed, the range passes. That makes a file-guard right for a
rule about the **result** of a piece of work ("every new file under `memories/` has
frontmatter"). It is never handed a `Pre*` event, and its checks read the commits,
never the working tree.

## Where it is enforced

`run` is the only command that asks a model or writes. Everything else verifies:

- **At Stop** (no model, nothing written). First **commit required**: an uncommitted
  change to a path some file-guard's `match` selects refuses the Stop with "commit
  these" (never commits for the agent; applies only to an agent that owns the tree;
  lets go after `stop_hook_block_cap` refusals of the same uncommitted set). Then
  each **tracked range** of the session (below) is verified like `sr-checks verify`,
  from the local results. **Stop shows failures only**: a stored FAIL (with its
  reasons), a rule that does not load, or a real error refuses it. A range nobody has
  judged yet is not reported at Stop, and passes it silently; run `sr-checks run`
  before pushing — the pre-push gate and CI `sr-checks verify` refuse an unjudged range.
- **Before a push**, the shipped `sloprail/gate/verify-before-push` gate (below), and optionally a git `pre-push` hook.
- **In CI**, `sr-checks verify` as a required status check (below). This is the
  backstop for anything a session did not track. A squash merge keeps the PR's
  verdict: `verify` reuses a stored verdict of the same rule judged over the same base
  and head **trees** (`(stored, same trees as <base>..<head>)`), PASS or FAIL, never
  across different trees. So squash-merge an **up-to-date** PR (merge main into it
  first); if main moved while it was open the push to main reads "not judged yet" —
  run `sr-checks run --base <before> --head <after>` for that push.

### Session folders and tracked ranges

The session keeps a registry of the folders it works in — its own repository, a
sub-agent's worktree, a repository a `git` command ran in (`git -C ../other commit`)
— and, per folder, the ranges of commits it answers for. When a folder is found its
current branch is **tracked automatically**, from where the work started (the merge
base with the default branch). The agent can change that:

```bash
sr-session refs list                                        # tracked and untracked ranges
sr-session refs track   [--folder D] [--base REV] [--head REF]   # track a range (replaces its base)
sr-session refs untrack --reason TEXT [--folder D] [--head REF]  # stop answering for it
```

Every branch the session commits on is tracked automatically, at every hook, and a branch
whose tip is a session-made commit that was never verified is tracked even with no new
commit. A folder the session first observes late only has its branch tips recorded: a branch
is tracked once its tip moves during the session, never merely because it already carries
commits (commits made before a folder was first observed are CI's to verify). Without an
explicit `--base`, the range's base is ALWAYS the merge base with the
remote default branch, read afresh at every Stop, whatever the session made, pulled or pushed:
a pull or a fast-forward push leaves nothing of that work in the local range. An explicit
`--base` is used exactly as given (and must be before the head).

Why so plain: CI is the hermetic guarantee. It verifies a pull request's range
(`merge-base(target, head)..head`) and a push event's `before..after`, so a session that pushes
straight to the default branch is caught by CI on that push. The local Stop is early feedback
only; it never has to tell the session's commits from upstream's.

Known limitation: tracking works by observing branch and HEAD movement. A commit created
without moving any local branch or HEAD (git plumbing such as `commit-tree`) and pushed
straight to a remote ref in one command is not tracked by observation; CI verify on the
pushed branch is the backstop.

Untracking is allowed freely — CI is the backstop — but the Stop lists what was
untracked, with the reason, and the range is tracked again by itself when the branch tip
moves. When a worktree is removed, its range moves to the root folder; if its branch is
gone too, the range stays pinned at the last tip (`refs/sloprail/pins/...`) and is still
verified. A sub-agent's ranges are verified at the root's Stop, unless
`enable_subagent_stop_check: true` is set in `.sloprail/config.yaml`. A background sub-agent
the session's registry holds as running (`sr-session agents list`) has its ranges left for a
later Stop (no note: unjudged ranges are not reported at Stop); one silent for `subagent_silent_after_minutes` (default 10) is named by the Stop, and
after `subagent_stale_after_minutes` (default 60), or once its session's process is gone, it is
stale and its ranges are judged like any other. CI verify covers every range either way. A folder's own `.sloprail` rules apply in it: gates judge the calls made there and
commit required covers its uncommitted work.

### Before a push: verify-before-push

The shipped gate `sloprail/gate/verify-before-push` is on by default. It refuses an
agent's `git push` until `sr-checks verify --base <merge-base(remote/default, sha)> --head <sha>`
passes for every ref the push would update, and the refusal names the `sr-checks run` that
judges the range. It fails closed when a ref, folder or base cannot be resolved. Switch it off
by listing `sloprail/gate/verify-before-push` under `disabled:` in `.sloprail/config.yaml`.

The verdicts live on the `sloprail/checks` branch, and a forged pass there would defeat
`verify`, so the shipped gate `sloprail/gate/checks-ref-sr-only` (also on by default) refuses
any agent git command that writes, moves, deletes or pushes that ref, or a file write into
its storage under `.git`. Reading it and running `sr-checks` stay allowed.

### Pre-push hook (for people, outside an agent)

```sh
#!/bin/sh
# .git/hooks/pre-push — judge what is about to be pushed
while read -r _ sha _ _; do
  [ "$sha" = 0000000000000000000000000000000000000000 ] && continue
  sr-checks run --base origin/main --head "$sha" || exit 1
done
```

`run` pushes the new results to `sloprail/checks` on `origin` itself.

### CI

```yaml
name: sloprail
on: { pull_request: { branches: [main] } }
permissions: { contents: read }
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }      # the merge base is needed
      - uses: actions/setup-go@v5
        with: { go-version: '1.25' }
      # Go, not install.sh: no release tarball carries sr-checks yet. Pin a commit or main.
      - run: |
          GOBIN="$HOME/.local/bin" go install github.com/sloprail/sloprail/services/sr-checks@main
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"
      - run: sr-checks verify --base origin/${{ github.base_ref }} --head ${{ github.event.pull_request.head.sha }}
```

`verify` fetches `sloprail/checks` from `origin` and reads it; it never writes. Plugins the project enables but CI has not installed are reported on stderr and their rules are not verified there (the exit status is unaffected). Make
the job a required status check. Red means some subject has no stored pass: run
`sr-checks run` over the same range and push.

`require: skill` and `require: context` belong to gates, not file-guards: a file-guard
is judged from the repository alone. A `require: citation` counts the `Sloprail-Cites-*` trailer on the commit that last
really changed the file (a whitespace-only or empty commit grounds nothing), which the repository alone can show.

### The rule-age floor

A rule judges only the work made after it came into force. Inside the range, a rule's
effective base is the later of the range's base and the parent of the commit that last changed
the rule's folder, so a rule added mid-branch applies from its add commit, and what came
before is not its debt. A rule that already stood at the base keeps the whole range (editing,
or deleting and re-adding, a rule mid-range is no way to skip judging earlier work). A rule
the branch's history does not carry (cut before the rule arrived) is in force from the date of
the commit that last changed it in the checkout. A rule with no commit anywhere, or one that
lives outside the repository (a plugin's), has no floor and judges the whole range.

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
The `subjects:` key (below) supplies another subject list without reshaping the payload.
The check results store each requirement row under its subject.

### `subjects:` — split a rule into units, each cached on its own

```yaml
match: "specs/**"
subjects: ./subjects.sh
checks:
  - judge: ./review.md.j2
```

`subjects:` is an optional script, resolved from the rule's folder. It is run with the changeset
payload on stdin and **no session** (no `SR_TRANSCRIPT`, no session id: `run` and `verify` both
run it, to compute the same keys). It prints a JSON array:

```json
[{"id": "billing", "files": ["specs/billing.md"], "fingerprint": "9f2c"},
 {"id": "auth",    "files": ["specs/auth.md"]}]
```

Each `id` is unique. A **file subject** names selected files (`files`), and its key is those
files' content **plus** its `fingerprint` when it gave one (the two are additive). A subject
that is not a file, an FQN say, names no `files`, and its `fingerprint` **is** the content part
of its key: with neither files nor a fingerprint it is refused (a load error, never an empty
key). The rule's requirements and checks then run once per subject, each handed that subject
(`.subject`), and each subject has its own stored verdict. Without `subjects:` the rule has one
subject, `changeset`, made of every selected file, and no fingerprint. A fingerprint names what
that subject's verdict depends on **besides its files' content** (a file a check opens with its
own tools, a version of an external spec): when it changes, only that subject is run again. It
must be session-independent and cheap. A rule with a `subjects:` script is always judged over
the range it is asked about (no effective base, below): the diff cannot see what a fingerprint
depends on. To judge each file on its own, have the script return one subject per file.

A `Changeset` payload, shaped in [events.md](events.md#changeset--what-a-file-guards-checks-receive).
A script loops over `.changeset.files[]` (a rule about one file at a time — size,
frontmatter — is that loop), reading whatever else it needs from `SR_TREE`
([environment.md](environment.md)); a judge renders `{{ changeset }}` and
`{{ change }}` ([judge-checks.md](judge-checks.md)).

## Seeing what a rule will be handed

```bash
sr-checks changeset --rule size-limit --base origin/main --head HEAD
```

prints JSON and **runs nothing**: no check, no judge, no verdict stored. Use it to
write a rule's `match`, script and rubric against real input, and to find out why a rule
selected (or missed) a file. `--rule` is the folder name (`size-limit`) or the qualified
name a refusal cites (`file-guard/size-limit`, `<plugin>/file-guard/size-limit`). Its
keys: `rule`; `base`, `head` (the merge base and head, as SHAs); `ruleHash` (a hash of the
rule's tracked `.sloprail` files as on disk — edit one and old verdicts stop applying);
`unresolvedCitations` (the `Sloprail-Cites-*` trailers whose quote did not resolve); and
`payload`, exactly what a check receives on stdin.

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
in the file-guard, which rules on the committed result (`sr-checks run`).
A script both halves need is written once, as a library that keeps no event-kind
logic (in the file-guard folder, `<script>-lib.sh`); each half keeps a thin entry
that reads its own input — the `Pre*` event in the gate, the `Changeset` in the
file-guard — and sources it (`. "$lib_dir/<script>-lib.sh"`; the gate's entry finds it at
`../../file-guard/<rule>/`).

**A gate does not fail closed on an unknown result by itself — the engine adds nothing.** A command, `sed`
or `git` edit whose result the engine cannot derive reaches the gate with
`resultKnown: false` and an empty `newContent`. A gate whose decision reads the
content must refuse it (see [The resultKnown discipline](#the-resultknown-discipline)),
or the write slips through to be judged only once it is committed.

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

## Cached verdicts

**Every check is cached by content**: a script, a judge and a requirement alike. Verdicts are
stored on the orphan branch `sloprail/checks` (zstd segments, never checked out), which `run`
pushes to `origin` and `verify` fetches, so another clone, another session and CI all read the
same results. One verdict is kept per **guard x subject**, a fact about **(rule, rule hash,
subject, fingerprint)** and nothing else: which session, agent, branch or range produced it is
provenance. The rule hash covers every script and template in the rule's folder; the
fingerprint is the sha256 of the subject's files' content (always), the citations' quotes (for
`require: citation` rules) and the subject's own `fingerprint` from `subjects:` when it gave one.
Never `prepare`'s output, the rendered prompt, a commit, its SHA or its message. The verdict
records each step's status and reason, so `sr-checks show` says which step failed and why.

- **A finished pass or fail with the same key is a hit**: `run` executes nothing (no script, no
  judge): after a rebase, a squash, a revert, or by another session. A stored fail is replayed,
  terminal until the input changes. A miss runs the steps in order, first refusal ends it, and
  stores the verdict.
- **The effective base advances on a pass** (a10n's `GetEffectiveBase`). A rule is judged over
  `effective_base..head`. Starting at the base you asked for, the base moves to the head of a
  stored COMPLETE PASSING evaluation (same rule hash, every subject of it passed) whose own base
  lies at or before the base reached so far and whose head is after it and an ancestor of the
  head being judged, and so on while one advances: passes over B1..H1 then H1..H2 reach H2, and a
  pass over a narrower range B2..H (B2 after B1) advances nothing, since B1..B2 was never judged.
  With none, the base you asked for. So only the change since the last pass is re-examined. A
  fail never advances it, so a refused change stays in the range (and its stored fail is
  replayed, not re-rolled) until it is fixed. It is computed from the stored runs alone, so
  `verify` (in CI too) finds the same base `run` did. `sr-checks show` lists the whole range you
  ask about instead.
- **Changed input is run again.** Editing a tracked file under the rule's `.sloprail` root, or changing `model`,
  starts the verdicts over.
- **A check that reads anything beyond its subject's files must declare it**, through that
  subject's `fingerprint` in `subjects:` (a file it opens with `SR_TREE`, an external spec's
  version). An undeclared dependency is served a stale verdict when it changes.
- A refusal is a complete fail verdict and is stored, whatever refused (a requirement, a
  script, a judge). Only an engine-internal error is incomplete: nothing is stored, the
  subject reads "not judged yet", and the next `run` starts that guard again from its first step.

**`sr-checks verify` only reads.** It never executes a script, a judge or a requirement: it
computes each subject's key (running the `subjects:` script, without a session), reads the
stored verdict and prints each subject's result. A key with no stored verdict is red ("not
judged yet, run `sr-checks run --base ... --head ...`"); a stored fail shows its reasons.

**Order.** Within a run, `require` entries and script checks that precede a rule's first
judge run before any judge starts.

**Rules run concurrently, scripts before judges.** The file-guards of one run are
evaluated at the same time, on a pool of 6 (set `SLOPRAIL_STOP_CONCURRENCY` to
change it; `1` is one rule at a time). Within a rule the checks still run in the
order you declared them and the first refusal ends the rule. A refusal from a cheap check
defers only THAT rule's own judges; every other rule's judges still run in the same run,
so their refusals are reported beside the cheap one. A run reports each rule's time on
stderr (`cheap checks`, `judges`, and the total).

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
