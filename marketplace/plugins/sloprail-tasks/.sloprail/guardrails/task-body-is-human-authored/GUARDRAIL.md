---
hooks:
  PreFileCreate:
    - matcher: path matches "^memories/tasks/[^/]+/[^/]+/TASK\\.md$"
      hooks:
        - type: command
          command: ./judge-body.sh
  PreFileUpdate:
    - matcher: path matches "^memories/tasks/[^/]+/[^/]+/TASK\\.md$"
      hooks:
        - type: command
          command: ./judge-body.sh
  PostFileCreate:
    - matcher: path matches "^memories/tasks/[^/]+/[^/]+/TASK\\.md$"
      hooks:
        - type: command
          command: ./judge-body.sh
  PostFileUpdate:
    - matcher: path matches "^memories/tasks/[^/]+/[^/]+/TASK\\.md$"
      hooks:
        - type: command
          command: ./judge-body.sh
---

# A task's body is the human's ask, cited, and nothing else

## The judge is not deterministic, and the borderline is the title

Measured on `outstanding-refusals-scope`, whose body is a verbatim quote plus a
citation and nothing else: five identical runs returned **three PASS and two
FAIL**. The disagreement is always about the same thing — whether a title that
names the subject is "framing the user did not state".

Two consequences worth stating plainly rather than discovering later:

**A borderline task will flap.** It passes, then refuses on the next cycle with
nothing changed. Since a refusal does not advance the read mark, the agent is
told to fix a body that was accepted moments earlier and for which no edit
exists that would make it more correct.

**Retrying is not the fix.** Re-running until it passes would make the rule a
coin flip dressed as enforcement. If the flapping matters, the rubric has to say
what a title may contain — the current text exempts "a title naming the ask" but
gives no test for when a title stops naming and starts asserting.

What is NOT in doubt is the clear-cut end: a body carrying invented rationale,
design, or acceptance criteria fails every time. The variance is confined to
bodies that are already almost entirely quotation.

## The first verdict is not the verdict

The rubric asks for one line. Models sometimes emit a verdict, argue themselves
out of it in the same sentence, and emit the opposite one. Taking `head -1`
meant the abandoned first draft won, and a correct task was refused by a judge
whose own final answer was PASS. Both the verdict and the reason are read with
`tail -1` for that reason.

## "A human typed it" is not "a human asked for it"

The deterministic half proves a cited line is a real user message: `type: "user"`
with STRING content, which excludes the array-content tool results an agent could
otherwise cite as its own authorisation.

It cannot prove the message is an ASK. A task in this repo cited a line where the
user had pasted a schema fragment —

```
status: to_do | in_progress | blocked
priority: P1|P2|P3
```

— which is genuinely typed by the human and genuinely not a specification of
anything. It passes the string/array test because that test asks who typed it,
not what they meant by it.

Closing this deterministically does not look possible: distinguishing pasted
reference material from an instruction is a judgement about meaning. The judge
half catches it in practice, since prose claiming to derive from a schema
fragment will not correspond to it. Recorded here because the deterministic half
should not be mistaken for more than it is.

The body of every `memories/tasks/<group>/<name>/TASK.md` must be derived from what the
user actually said. It carries at least one citation of the form
`<absolute-path>.jsonl:<lines>` naming real user messages in a session
transcript, and its prose must correspond to those messages — *that and nothing
else*.

## Why this rule protects the oracle rather than an artifact

Every other rule in this set protects an artifact: the frontmatter is
well-formed, the evidence resolves, the review is honest. This one protects the
thing all of those are checked *against*.

The failure it prevents has no detector once it has happened. An agent
implements 70% of an ask, edits the task body to describe that 70%, and from
then on every verification in the system passes — the work matches the spec,
because the spec was rewritten to match the work. The sibling rule
`task-evidence-resolves` does not catch it: the transcript proof is real, it
just proves that the wrong thing was completed correctly.

So the specification is at once the most valuable object in the system and the
one an agent is most incentivised to soften. That asymmetry is the whole rule.
The user's own statement of it:

> "This way we protect the actual task content from being sloped over time."

An agent may flush an update onto the *result*. It may not edit the ask.

## The two stages, and why the order carries the argument

**1. Deterministic, first, no model.** Does the body carry a citation at all,
and does every cited line name a genuine user message? A real user message is a
transcript entry with `type == "user"` whose `message.content` is a **string**.
Tool results are *also* `type: "user"` and carry an **array** — so keying on the
type alone would let an agent cite its own tool output as the human's ask, which
is precisely the substitution this rule exists to prevent. It is the cheapest
possible forgery and it would defeat the entire rule, so it is the one thing the
deterministic half is built around.

That check is `cite_check_user_message` in the sibling rule's `citations.sh`,
which this hook **sources** rather than reimplements. Two rules that disagreed
about what line 40-60 means would produce a task satisfying one and refused by
the other, with no edit that satisfies both.

**2. Judged, only if stage 1 passed.** An `sr-agent` call asking whether the
body actually *corresponds* to the cited messages, and whether it contains that
**and nothing else**.

Cheap gates expensive. Never spend a model call to learn something a string
comparison already settled — the same ordering the engine uses for
fingerprint-skip revalidation, for the same reason. A body with no citation
never reaches the judge; neither does one citing a tool result. In both cases
the deterministic half has already produced the correct refusal, and a model
would only be asked to rediscover it more slowly and less reliably.

## "And nothing else" is the load-bearing clause

This is the half a naive implementation drops, and dropping it makes the rule
decorative.

A body carrying a valid citation and *also* three paragraphs of agent-authored
elaboration — inferred requirements, helpful sub-goals, a suggested approach,
acceptance criteria nobody asked for — is content slopped around a legitimate
citation. Every word of it is unattributable, and the next agent reading the
task cannot tell which sentences are the human's ask and which are a previous
agent's guess. Slop enters exactly here: not by contradicting the user, but by
*adding to* them under cover of a real reference.

So the judge's question is not "is this supported?" — almost anything can be
argued to be supported — but **"is this ONLY this?"** The rubric states it that
way deliberately, and `RUBRIC.md` holds it so the standard can be tightened
without a diff to shell quoting.

Structural restatement of the user's ask is not slop: a heading, a bulleted
split of a compound sentence, the ask reworded to be shorter. The judge is told
so explicitly, because a rule that refuses every act of formatting would be
turned off within a day, and a rule that is turned off protects nothing.

## Pre and Post, both bound

The source requirement is emphatic that this rule binds **Pre**: an edit to the
ask must be refused *before* it lands, because a Post refusal reports damage
already done to the oracle and the agent's remedy would be to edit the task
again — which is the prohibited act itself.

The Post bindings are here anyway, and they are not a hedge against that
argument. Pre cannot see everything. A task written by a command whose resulting
bytes the engine cannot derive produces **no Pre event at all** — for
`PreFileUpdate` this is visible as `resultKnown == false`, and for a create by
such a command there is simply no event. On that path the choice is not between
Pre and Post; it is between Post and no enforcement whatsoever. A tree diff sees
the file however it was made.

So: Pre refuses the derivable writes before they land, which is the whole of the
normal path, and Post catches what was written around it. The hook defers
(exits 0) on a Pre event whose bytes it cannot see, precisely so the Post
binding can judge what actually landed rather than a verdict being reached on
bytes nobody has read.

`PreFileDelete`/`PostFileDelete` are deliberately **not** bound. A delete
carries only a path — there is no body to judge — and deleting a completed task
is how the review rule finishes its job.

## What fails open and what fails closed

The **verdict** fails closed. A judged violation refuses.

The **plumbing** fails open: no `sr-agent` on PATH, a timeout, an unparseable
answer. A model call flakes for reasons that are not evidence about the file,
and under fail-closed a single flake wedges a session that the agent cannot
un-wedge by fixing anything — there is no edit to the task that makes a missing
binary appear. Each fail-open branch says so on stderr, naming the rule, so a
silently unjudged task is still visible rather than merely absent.

The **deterministic half fails closed throughout**. It reads the event and the
filesystem; nothing in it can flake for a reason unrelated to the task, so a
failure there is a defect to surface rather than to permit past.

The judge's timeout is 25s, and it must stay comfortably under the engine's
30s `hookTimeout`. If it did not, the engine would kill the hook first and read
the kill as a refusal — the fail-open branch below could never run, and a model
that was merely slow would refuse the write. The fail-open would be a comment
describing code that is unreachable.

## Scope: the body, not the frontmatter

This rule reads only the prose after the frontmatter. The frontmatter is
`task-evidence-resolves`'s subject and is checked there against `task.cue`;
re-checking it here would be two rules drifting apart. A body-citation is
therefore written **in the body**, not in `observations` — those two lists mean
different things. `observations` cite proof that the work *happened*; a body
citation names where the human *asked* for it.

A task whose body is empty or absent is refused rather than permitted. An
uncited ask is exactly the thing that cannot be told apart from an invented one.
