---
hooks:
  PreFileCreate:
    - matcher: path startsWith "memories/topics/" or path startsWith "memories/decisions/"
      hooks:
        - type: command
          command: ./require-skill.sh
  PreFileUpdate:
    - matcher: path startsWith "memories/topics/" or path startsWith "memories/decisions/"
      hooks:
        - type: command
          command: ./require-skill.sh
---

# Required context is a precondition, not a reminder

Some folders hold artifacts that have a shape. A topic is a workspace with a
thesis and units under it; a decision records what was considered and why. The
shape is written down in a skill, and an agent that has not read the skill does
not produce the shape — it produces something plausible, in the right folder,
that has to be found and redone later.

This rule makes reading it a precondition. Before a file under a guarded prefix
is written, the skill that prefix requires must already have been loaded in this
session. No skill loaded, no write: the write is **denied**, not warned about.

## What is checked

The session's own record — its trajectory. The rule looks for a `Skill` tool_use
naming the required skill, among the entries the agent produced before this
write.

That is the whole of it, and the narrowness is the point. Three things could
stand in for "the agent has the context":

- **What the agent says.** It can say it read the skill without having read it,
  and saying so costs nothing. Not checked.
- **What the artifact looks like.** Content resembling the skill's output is
  evidence the skill was followed, but a model that has seen one topic file can
  produce a convincing second one from memory. Not checked.
- **What the agent did.** A `Skill` tool_use is in the record or it is not.
  There is no wording that puts it there. **This is what is checked.**

The name is read from `.input.skill`, which the spec declares — see
`SkillToolInput` in the claude-code dependency. That mattered: the script used to
hedge, `.input.skill // .input.command`, because nothing said which field a
`Skill` tool_use carries. Measuring real transcripts settled it — `skill` on all
664 calls, `command` on none — and the hedge is gone. A fallback to a field that
never occurs is not caution; it is a second way to match the wrong thing quietly.

Sub-agent entries do not count. A skill loaded inside a delegated sub-agent was
loaded on a different line of work than the one now writing the file, and the
agent doing the writing did not read it.

Whether the skill's invocation *succeeded* is not checked, only that it was
made. The rule is about the agent having reached for the context, and a skill
that failed to load is a broken environment rather than an agent that skipped a
step — a distinction the agent cannot fix by rewriting its file.

## Which folders, and which skill

| Path prefix            | Required skill      |
| ---------------------- | ------------------- |
| `memories/topics/`     | `document-topic`    |
| `memories/decisions/`  | `document-strategy` |

Both halves of that table live in `require-skill.sh`. The matcher in the
frontmatter decides only *whether* the hook runs; *which* skill a given path
demands is a second question the matcher cannot answer, because a matcher reads
the event's own fields and the mapping is this rule's, not the event's.

To adapt this to your project, change the prefixes in the matcher and the `case`
in the script together. A prefix admitted by the matcher and missing from the
script's table is **permitted** — the rule speaks for the prefixes it names and
declines to guess at the others.

## What happens on a refusal

The agent is told which skill to load and to retry the write. That is the entire
remedy, and it is one action:

> SKILL REQUIRED: writing under 'memories/topics/x/TOPIC.md' requires the
> 'document-topic' skill to have been loaded first, and this session's record
> holds no Skill tool_use naming it.

A refusal that told the agent only that it had failed would leave it guessing,
and a guessing agent writes the file again somewhere else.

## Failing closed

Every path through the hook that does not *establish* the skill was loaded
refuses. Not only the case where the record says it was absent — also the cases
where the record could not be read at all, or where the engine did not say which
record this session keeps.

That is deliberate and it is the opposite of convenient. A precondition that
permits when it could not check has not checked; it has consented, and it looks
identical from outside to one that checked and approved. The engine holds the
same line from the other side: a hook that exits non-zero has refused, and a
hook that could not be run at all is a refusal too.

The cost is real — a broken environment blocks work instead of quietly allowing
it. That is the cheaper failure. A blocked write is visible in the next thing
the agent says; a silently permitted one is found weeks later in the wrong shape
in the wrong folder.

## Not yet runnable: SR_TRANSCRIPT

**On every branch as of this writing, this rule refuses every write under a
prefix in its table** — the check it would run cannot run, and it fails closed
rather than permitting. Writes elsewhere are unaffected: a path the table does
not name is permitted before the trajectory is ever consulted, so the rule keeps
its scope even while it cannot do its job inside it.

It is written against an interface the engine does not yet offer.

Reading the trajectory means calling `sloprail session query`, which is told
which record to read via a `transcript_path` on its stdin payload. A hook is
handed `{event, guardrailDir}` and nothing naming the session's record, so it
has no path to pass.

`impl/hook-env` is the branch that gives hooks an environment at all —
`SR_GUARDRAIL`, `SR_SESSION_ID`, `SR_WORKSPACE` — and it is necessary but **not
sufficient** for this rule. `SR_SESSION_ID` carries the *stable* session id,
which is the uuid of the conversation's root record, deliberately not the
harness's own id and therefore not the transcript's filename. Building a path
from it lands on a file that does not exist.

Worth naming precisely, because it looks derivable and is not: joining the
encoded workspace and `SR_SESSION_ID` produces a plausible path that silently
points at no file, rather than an error.

### What closes it

**One more variable: `SR_TRANSCRIPT`, the record's path, set beside the three
`impl/hook-env` already sets.** That is the whole fix, and the engine already
holds the value — it parses `transcript_path` off the hook payload into
`HookPayload.TranscriptPath`, and builds the `hookScope` from that same payload.
So it is a field on the scope, a line in the environment it constructs, and the
same not-a-path sentinel `SR_WORKSPACE` already uses for the case where the
engine could not resolve one.

It is filed as `hook-transcript-path` in the backlog, at P0, which carries the
argument in full.

The alternative — a `--transcript` flag on `sloprail session query` — would also
work and is deliberately not the plan. A flag puts the record's identity in the
hook's hands, so a hook could name a session it was not running in; every other
piece of scope the engine gives a hook is environment rather than argument for
exactly that reason. It would also leave the derivable-looking `SR_SESSION_ID`
trap in place for the next author.

### What the rule does until then

Exactly what it does now, and nothing about the script changes when the variable
arrives — it is already written against it. Every write under a prefix in the
table is refused by name, saying `SR_TRANSCRIPT` is unset. That is the correct
behaviour for a precondition it cannot check, and it is why this rule should be
enabled only once that variable exists. Writes elsewhere are unaffected, as
above.
