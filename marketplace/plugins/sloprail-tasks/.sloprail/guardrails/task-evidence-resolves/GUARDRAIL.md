---
hooks:
  PreFileCreate:
    - matcher: path matches "^memories/tasks/[^/]+/[^/]+/TASK\\.md$"
      hooks:
        - type: command
          command: ./check-task.sh
  PreFileUpdate:
    - matcher: path matches "^memories/tasks/[^/]+/[^/]+/TASK\\.md$"
      hooks:
        - type: command
          command: ./check-task.sh
  PostFileCreate:
    - matcher: path matches "^memories/tasks/[^/]+/[^/]+/TASK\\.md$"
      hooks:
        - type: command
          command: ./check-task.sh
  PostFileUpdate:
    - matcher: path matches "^memories/tasks/[^/]+/[^/]+/TASK\\.md$"
      hooks:
        - type: command
          command: ./check-task.sh
---

# A task's frontmatter is valid and its citations resolve

Every `memories/tasks/<group>/<name>/TASK.md` must satisfy
`.sloprail/schemas/task.cue`, and every citation in its frontmatter must point
at something that is actually there.

This is the deterministic floor of task management. It spends no model call and
answers only questions a string comparison or a filesystem lookup can settle. It
runs before the judged rules so neither of them is ever asked to reason about a
task that is malformed — cheap gates expensive, the same ordering the engine
uses for revalidation, and for the same reason: never spend a model call to
learn something `sed -n` already settled.

## What the schema pins, and what it structurally cannot

`task.cue` is closed and its `status` is a disjunction of five literals, so an
invented status or an invented field is refused at the point of writing.

What a schema cannot ask is whether a string that LOOKS like a citation names
anything real. CUE sees `"/a/b.jsonl:12-30"` as a string matching a pattern; it
has no filesystem and no opinion about whether line 30 exists. So the regex pins
the shape and this hook pins the substance — the division
`frontmatter-transcript-path` already draws for `transcript_path` in this repo's
sibling project.

The schema being closed was **measured, not assumed**. CUE's default is open: a
first draft carried a comment claiming the schema was closed while a task with
an invented `sneaky:` key validated cleanly. It is wrapped in `close()` now, and
the extra-key case is one of the seven the schema was checked against.

## The three questions the hook asks

For every citation in `observations` and `artifacts`:

1. **Does the path resolve** to a readable file.
2. **Does every cited line exist** in it — a range past the end of the file is
   refused, because a reviewer following it lands on nothing.
3. **For `observations`, is the cited entry a real record** rather than
   something the agent could have authored. See below.

Each failure names the citation and says which of the three it was, because the
fixes differ: a wrong path, a wrong range and a reversed range are three
different mistakes, and one message saying "invalid citation" would send the
agent looking in the wrong place for all three.

## Why `in_review` requires the evidence and the schema does not

`task.cue` marks `observations` and `artifacts` optional, which reads at first
like a hole. It is not. CUE could express the conditional, but the moment it did
the failure would be a unification conflict rather than a sentence about
evidence. The requirement is enforced here, where the refusal can say what to
attach and why.

A task moving to `in_review` without both lists is refused. That is the force of
the intake rule — *"if the task is moved to done, then it should mandatory
contain ranges from JSONL transcript that represent proofs"* — and the word
carrying it is **mandatory**.

## Pre and Post, both bound

The Pre bindings refuse the write before it lands, which is right for a rule
about permission to act.

The Post bindings exist because Pre cannot see everything: a task written by a
command whose bytes the engine cannot derive produces no Pre event at all, and
the rule would never run. A tree diff sees the file however it was made. The
trade is that a Post refusal arrives after the bytes are on disk, so the agent is
told to fix it rather than prevented from writing it — for the underivable tier
that is the honest answer, and the alternative was no enforcement on that path.

## Failing closed

This rule fails closed on everything, and unlike the judged rules it needs no
fail-open override. It reads the event, the schema and the filesystem — all
local, none of them a model call that can flake for reasons that are not
evidence about the task. If `jq` or the schema is missing, that is a defect to
surface rather than to permit past.
