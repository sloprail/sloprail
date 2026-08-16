---
hooks:
  TurnEnd:
    - hooks:
        - type: command
          command: ./check-open-tasks.sh
---

# A turn may not end while work is still open

## It refuses on inherited state, and that was accepted deliberately

The check is "does any task sit in `to_do` or `in_progress`" — not "did this turn
leave one there". So the first turn after enabling the rule is refused
unconditionally if a backlog already exists, whatever the agent did, and even if
it did nothing at all.

That is the **gate** reading — "this repository does not rest while work is open"
— rather than the **discipline** reading — "do not walk away from work *you*
touched". The two differ only when work is inherited, and the difference is
whether the rule costs one triage session or charges every future session for a
backlog it did not create.

The gate is what was specified and what is built. It was proven the hard way: the
subagent that authored this rule was refused by it on a real `Stop` against the
real tree, which is a firing proof no fixture could give. It declined to clear
the refusal by re-statusing tasks that were not its own — the right instinct for
an agent, since calling somebody else's `to_do` deferred is a claim about
priority rather than a status update.

The repository owner then made that call explicitly, and the twelve open tasks
were moved to `backlog` on their instruction. `backlog` is the status that exists
precisely so "not now" has somewhere to live that is not `to_do`; parking work
there is a scheduling decision, and it belongs to whoever owns the work.

At the end of every cycle, every `memories/tasks/*/*/TASK.md` is read. If any of
them is `to_do` or `in_progress`, the turn is refused and the agent is told
which tasks, in which status, and what the four ways out are.

The rule exists because a status field nobody reads is a comment. An agent that
opens a task, works next to it, and ends the turn leaves a `to_do` sitting in
the tree indistinguishable from one filed three weeks ago — and the next
session inherits a backlog it cannot tell apart from a work queue. Making the
turn boundary the place the question is asked is what keeps the status honest:
you may not walk away from a task without first saying, in the file, what
happened to it.

## The refusal set is exactly {to_do, in_progress}

The schema's five statuses split into two groups, and the split is not
"finished / unfinished" — it is **may this rest unattended**.

| status | at turn end | why |
|---|---|---|
| `backlog` | rests | filed deliberately for later. `backlog` exists precisely so "not now" has somewhere to live that is not `to_do`; refusing on it would collapse that distinction and make the schema's own comment false. |
| `blocked` | rests | cannot proceed, and the body names the blocker. There is nothing the agent can do this turn, so refusing would be an instruction it cannot follow. |
| `in_review` | rests | the agent is done and has attached evidence. The review guardrail owns it now — this rule refusing on it would be two rules holding one task, and the agent would be told to act on something whose next move is somebody else's. |
| `to_do` | **REFUSED** | scheduled and not started. Scheduled for *when*, if not this turn. |
| `in_progress` | **REFUSED** | being worked — by whom, if the turn is ending. |

`done` is not in the table because it is not in the schema. A task the reviewer
approves is deleted folder and all, so there is no terminal status to reach and
no way for this rule to be satisfied by writing one.

## TurnEnd carries no fields, so the hook establishes its own subject

Bound to `TurnEnd`, and that binding has **no matcher** — not omitted for
brevity. Naming any field on this kind is refused at load, and this was
confirmed rather than assumed: a first draft carried
`matcher: path endsWith ".md"`, and `sr-session start` answered

```
  - event "TurnEnd" binding 0: matcher "path endsWith \".md\"":
    unknown name path (1:1) — TurnEnd carries no fields
```

That absence is the design of the kind: the end of a cycle is about the cycle,
not about one file. So the hook has no subject handed to it and goes and finds
one — it walks `$SR_WORKSPACE/memories/tasks/*/*/TASK.md` and reads each one's
frontmatter.

**No state is used.** The two-halves pattern — per-file hooks recording into
`sr-session state` for a cycle hook to judge — is the usual shape for a
TurnEnd rule, and it is the wrong shape here. It answers "did something happen
during this turn", which needs a turn-scoped stamp to be sound. This rule asks
"what does the tree look like right now", and the tree is the answer to that,
already scoped by being the present. Recording task writes into state would add
a stamp, a helper both halves must agree on, and a whole class of
permissive-direction bugs, to reproduce a fact `ls` already has. A task nobody
touched this turn is still open, and this rule must still refuse on it — which
is exactly what a recorder-based version would miss.

## The frontmatter is read by `sr-file validate --emit`, never parsed here

The product's own command owns what a document's frontmatter is. A hand-rolled
`awk '/^---$/'` here would be a second opinion about where frontmatter ends, and
the day the two disagree the rule silently reads the wrong bytes — a status
lifted out of a `---` inside the prose body, say. So the status comes from

```sh
sr-file validate "$f" --schema "$schema" --emit | jq -r .status
```

`--emit` prints the validated frontmatter as JSON on success and **nothing** on
failure, which makes the invalid case detectable without parsing an error
message.

## A task that fails the schema is skipped, and this is deliberate

`--emit` prints nothing for a document that did not validate, so this rule
cannot learn such a task's status even in principle. The choice is between
refusing on it (a second rule reporting a schema violation) or staying silent.

**It stays silent, and reports the count to stderr only.** The reason is
ownership: `task-evidence-resolves` already refuses an invalid TASK.md at the
moment it is written, with a message that names the field and prints the five
legal statuses. Refusing here as well would hand the agent two refusals for one
mistake, from two rules, at two different moments — and this one's message would
be the worse of the two, because it does not know what was wrong, only that
`--emit` printed nothing.

There is a hole in this and it is worth naming rather than hiding: the real
tree at the time of writing contains four tasks with `status: done`, a status
the schema does not have. They predate the schema, so no Pre hook ever judged
them, and this rule will not report them either. They are invisible to both
rules — but a task carrying an invented status is a schema problem, and the
place to fix it is the schema rule's Post binding or a one-off sweep, not a
turn-end rule inventing a second opinion about validity. Skipping is also the
*conservative* direction for a rule that fires every cycle: a malformed file
that cannot be parsed does not get to wedge every turn in the session.

## Fail closed on the logic, open on the plumbing

The split matters more here than anywhere, because this kind fires on every
cycle and a bug wedges the session wholesale rather than one file.

**Closed on the logic:** an open task is a refusal. That is the entire rule, and
there is no path where an open task is permitted.

**Open on the plumbing:** no `$SR_WORKSPACE`, no `memories/tasks/` directory,
`sr-file` or `jq` missing — each exits 0 and prints to stderr which path was
taken. None of those is evidence that the work is finished, and refusing on them
produces a session the agent cannot un-wedge by doing anything to its tasks: it
would be told to close tasks it cannot see, forever. A missing tool is a defect
in the environment to surface loudly, not a verdict about the tree.

The one asymmetry: a directory that exists but holds no tasks permits, and that
is logic rather than plumbing. No tasks means no open tasks.

## The refusal names every task, because a refusal here repeats

A refusal at `TurnEnd` does not advance the cycle's read mark, so an unsatisfied
rule is re-reported next cycle rather than scrolling away. The message is
therefore something the agent will see repeatedly until it acts, which makes its
content load-bearing in a way a one-shot refusal's is not.

So it names **every** open task and its status — not a count. "You have 3 open
tasks" tells an agent something is wrong and gives it nothing to do; a list of
paths with statuses is a work queue. And it names all four exits explicitly —
finish it, `blocked` with a named blocker, `backlog`, or `in_review` with
evidence attached — because three of those four are ways to end a turn honestly
*without* doing the work, and an agent that does not know they exist will either
do work it should not or fake a status the schema refuses.
