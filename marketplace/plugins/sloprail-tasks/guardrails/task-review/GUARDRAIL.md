---
hooks:
  PostFileCreate:
    - matcher: path matches "^memories/tasks/[^/]+/[^/]+/TASK\\.md$"
      hooks:
        - type: command
          command: ./review-task.sh
  PostFileUpdate:
    - matcher: path matches "^memories/tasks/[^/]+/[^/]+/TASK\\.md$"
      hooks:
        - type: command
          command: ./review-task.sh
---

# `in_review` is a claim; this rule is what judges it

A task whose status is `in_review` is asserting that the work is finished and
that the evidence it carries proves it. This rule reads the task's stated
outcome, reads the cited observations and artifacts, and asks a model whether
the evidence actually substantiates the claim.

**Rejected** — the turn is refused, naming which citation failed and why. The
status stays `in_review`; the agent fixes the evidence and comes round again.

**Approved** — the turn is refused too, with a different instruction: delete the
task folder and commit solely that deletion. Why a refusal rather than a permit
is the longest argument in this file; see "The approve path" below.

## There is no `done`

`task.cue`'s `status` disjunction has five literals and `done` is not one of
them. A task the reviewer approves is DELETED, folder and all, and that deletion
is committed on its own. So `in_review` is the terminal *writable* status, and
the only thing that can follow it is either a corrected `in_review` or an
absence.

That is what makes this rule load-bearing rather than decorative. Without it,
`in_review` is a status an agent can write about its own work and nothing ever
tests. The schema stops an agent writing `done`; this stops it writing
`in_review` and walking away.

## What this rule does NOT check

Everything deterministic. `task-evidence-resolves` already refuses a task whose
frontmatter fails the schema, whose `in_review` status is missing either list,
or whose citations name a path that does not exist or a line past the end of a
file. This rule does not re-run any of that.

It sources that rule's `citations.sh` — one copy, not a second parser — and uses
it for one thing only: **expanding** citations into the actual bytes to put in
front of the judge. `cite_path` and `cite_ranges` split a citation; `cite_lines`
turns `12-30` into the line numbers to read. If two rules disagreed about what
`40-60` means, a task would satisfy one and be refused by the other with no
spelling that satisfies both.

The one place this rule re-runs a resolution check is as a **pre-flight**: if a
citation does not resolve *here*, the deterministic rule is either not installed
or was bypassed, and there is nothing to show a judge. That refuses without a
model call and names the citation, which is exactly the class of refusal the
task brief asks for — `artifacts[1] /abs/p.go:10-60 — line 43 is past the end of
the file (42 lines)`. Cheap gates expensive: never spend a model call to learn
something `sed -n` already settled.

## The event, and why Post only

Bound to `PostFileCreate` and `PostFileUpdate` on `^memories/tasks/[^/]+/[^/]+/TASK\.md$`.

**Post, because the bytes on disk are what actually landed.** A `Post` event is
established by diffing the tree against the session baseline, not by trusting an
announced tool call, so a task written by `Bash`, by a heredoc, or by a `sed -i`
whose result the engine cannot derive still produces one. A judged rule that
could be evaded by writing the file a different way would be worse than no rule,
because it would look installed.

**A Pre binding was considered and deliberately rejected.** Three arguments,
none of them close:

1. `PreFileUpdate` carries `result`/`resultKnown`, and `resultKnown` is false for
   exactly the writes an agent would use to slip a claim past a judge. The
   skill's own advice is that a judge bound to an update should defer when the
   result is unknown and let Post judge what landed — which leaves Pre judging
   only the cases Post already judges, at the cost of a second model call.
2. `PreFileCreate` has no prior file, so a *newly created* task can only have
   evidence that predates the task — legitimate, but it is the same judgement
   Post makes a moment later on the same bytes.
3. A model call on the Pre path pays twice for one verdict. A judged rule costs
   real seconds; doubling that for a case Post covers is a tax on every task
   write in the repo.

The cost of Post-only is that the bytes are on disk when the refusal arrives.
For this rule that is not a cost at all: **the refusal is an instruction to do
something next** (fix the evidence, or delete the folder), not a veto on the
write having happened. A `Post` refusal is reported as a blocking error on the
cycle and the read mark does not advance, so an unfixed task is judged again
next cycle rather than scrolling away — which is precisely the loop this rule
wants.

No `PreFileDelete`/`PostFileDelete` binding. The approved outcome is a deletion,
and a rule that fires on the deletion it just demanded would have to work out
whether it had demanded it. It does not need to: a deleted task has no
frontmatter to judge, and `task-evidence-resolves` does not bind delete either.

## The approve path — the decision, and the argument I rejected

The user's stated design is: *"Deletion happens already in this reviewer agent.
It will delete and commit solely this change. If it was approved by reviewer."*

**Implemented: the hook REFUSES on approval, with an instruction to delete the
folder and commit solely that deletion.** The hook itself runs no `git`.

### Why not have the hook do it

The rejected design is that on APPROVED the hook runs `git rm -r` on the task
folder and `git commit` with a message naming it, then exits 0. It is a coherent
design and it is what the user described. Four arguments against it, recorded
here so switching back is an informed choice rather than a rediscovery:

1. **A guardrail's outputs are permit and refuse.** That is the whole contract:
   stdin one event, exit status the verdict. A hook that mutates the tree and
   writes to git history is doing something the contract has no vocabulary for,
   and nothing downstream — not the engine, not the agent, not the user reading
   a refusal — is told it happened. The sibling rules in this repo all read;
   this one would be the only one that writes.

2. **The deletion is irreversible and the trigger is a model.** `git rm -r` on
   the task folder destroys a human-authored task body on the strength of one
   `size-md` verdict. Every other judged rule in this codebase fails open on its
   plumbing precisely because a model call flakes for reasons that are not
   evidence about the file. A flake that permits is recoverable; a flake that
   deletes is not. There is no fail-open direction for a deletion.

3. **It collides with `task-body-is-human-authored`.** A guardrail that edits or
   removes the file it guards is the shape that rule exists to refuse. Even if
   the sibling rule's matcher does not happen to fire on a deletion today, a
   validation hook rewriting its own subject is the pattern, and encoding it
   here would make the pattern respectable.

4. **A commit from inside a hook lands in whatever the working tree happens to
   be.** A hook runs mid-cycle, with the agent's other unstaged work possibly
   present. "Commit solely this deletion" requires knowing that nothing else is
   staged, and a hook that discovers something else *is* staged has no good
   move: stash the agent's work, refuse anyway, or commit something the user did
   not ask for. The agent — which knows what it just did — can do this
   correctly. The hook cannot.

### Why the approved path is a refusal and not a permit

Because a permit would make the rule a no-op on its success path, which is the
failure the authoring skill names first. An approved task that is merely
permitted sits in `in_review` forever: the next cycle either re-judges it (a
model call per cycle, forever, for a task that is finished) or, once
revalidation has seen those bytes, never judges it again — and the task is a
tombstone nobody deletes.

Refusing closes that. The refusal carries the exact commands, the read mark does
not advance, and the same task comes back next cycle until the folder is gone.
**The one thing that clears an approved review is the deletion**, which is the
lifecycle the user specified — the hook just is not the thing holding the knife.

The cost is that "approved" is reported through the refusal channel, which reads
as failure at a glance. The message opens with `TASK APPROVED` and says the work
is accepted, so the agent is not told it did something wrong. This is the honest
trade and it is stated in the refusal itself.

## The judge

`sr-agent --model size-md`, prompt built from `RUBRIC.md` beside this file, read
via `guardrailDir` off stdin. The standard is not in the shell: changing what
counts as substantiated evidence should be a diff to a prompt, not to quoting
and heredocs.

`size-md` rather than a named model: a size alias resolves under every harness,
so the rule does not assume the harness that will run it. This is a
substantiation judgement over quoted evidence, not a creative one — `size-md` is
the right tier and was chosen for that, not for cost.

The judge is given, per citation, the **actual cited lines**, not the file. A
judge handed a whole 4000-line source file and asked whether lines 10-60
substantiate a claim will read the whole file and answer about the file. Slicing
to what was cited is also what makes the "observations show the test command but
not its result" refusal possible: if the cited range genuinely stops before the
output, the judge sees a range that stops before the output.

Lines are truncated (per-citation and in total) so one task citing a
10,000-line range cannot produce an unbounded prompt. Truncation is announced
inline to the judge, so a judge seeing a truncated slice knows it is looking at
part of one and can say the evidence was not legible rather than inventing a
verdict about bytes it was not shown.

### Fail open on plumbing, closed on the verdict

**Plumbing fails OPEN** — `exit 0`, with a line on stderr naming the path taken:
no `sr-agent` on PATH, `jq` missing, `citations.sh` or `RUBRIC.md` not found,
the call timing out, an empty or unparseable verdict, a verdict whose `decision`
is a word this rule does not know. A model call flakes for reasons that are not
evidence about the task, and under fail-closed one flake wedges a session the
agent cannot un-wedge by fixing anything.

**The VERDICT fails CLOSED.** `REJECTED` refuses. `APPROVED` also refuses, for
the separate reason above. The only `exit 0` on a verdict is the one that never
happens: there isn't a third verdict word that permits.

To restore the engine's fail-closed default, change the `exit 0` lines marked
`FAIL-OPEN` in `review-task.sh` to `exit 1`.

### The timeout is 20 seconds and must stay under 30

The engine's `hookTimeout` is 30s. A judge given a longer budget can never reach
its own timeout: the engine kills the hook first, reads the kill as a refusal,
and the fail-open branch below it never runs — so a model that was merely slow
refuses correct work. The sibling repo's judges carried 60s and 90s timeouts
for exactly this reason and both had a fail-open that could not fire. 20s leaves
room for the script's own work either side of the call.

## What this rule cannot do

It cannot tell whether the work is *good*, only whether the evidence
substantiates the claim that it was done. A green test that tests nothing is a
green test; the judge is asked about the link between claim and evidence, not
about the engineering. That is the honest scope, and widening it would be asking
a `size-md` judge to review code it can see 50 lines of.

It also cannot verify that a cited observation is a genuine transcript entry
rather than a `.jsonl` the agent wrote itself. `citations.sh` has
`cite_check_user_message` for the stricter question, but it asks whether an
entry is a real *user message*, which is the wrong question for an observation —
an observation is a tool result, and tool results are exactly what that function
rejects. A "this line is a real assistant/tool-result entry the agent could not
have authored after the fact" check belongs in `citations.sh` beside it and does
not exist yet. Until it does, a determined agent can fabricate a transcript. The
judge sees the content and can notice implausibility, but that is a model
noticing, not a check.
