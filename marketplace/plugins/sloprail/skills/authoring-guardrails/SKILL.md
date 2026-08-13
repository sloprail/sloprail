---
name: authoring-guardrails
description: Use when adding, fixing, or turning off a guardrail in a project that has sloprail installed — a rule under .sloprail/guardrails/ that refuses an agent's action. Covers whether a rule is worth writing, what makes one that actually fires, and how to prove it did.
---

# Authoring Guardrails

## Get the format from the binary, not from here

```
sloprail guardrail help
```

That prints the declaration's shape, every event kind this build can produce
with the fields each carries, the matcher operators, and the hook contract. It
is generated from the modules the binary registered, so it is what this build
actually has. This skill does not restate any of it — a second copy would be the
one you trust when the two disagree.

Read it before writing. You cannot guess the kinds: they are per-build.

## The failure this exists to prevent

A guardrail that never fires is worse than no guardrail. No guardrail is an
absence someone can notice. A rule that loads cleanly, sits in the project
looking enforced, and silently admits everything is a project believing it is
protected while it is not.

Almost every way to get this wrong produces that outcome rather than an error.
So the work is not "write a plausible declaration" — it is "write one, then
prove it refused something."

## Is this rule worth writing

A guardrail earns its place when all of these hold.

**The violation is real and recurring.** You have seen it happen, or the user
has named it. A rule against something nobody does costs every session and
catches nothing.

**A machine can tell.** The check runs in a hook script with the event's fields
and, if needed, the session record. "Writes under `memories/decisions/` without
having loaded `document-strategy`" is decidable. "The code is well designed" is
not — unless you are handing the body to a judge, which is what the prose is
for.

**Refusing is the right response.** A `Pre` kind prevents the action. If the
honest response is "note it and move on," a guardrail is the wrong instrument:
it only knows how to refuse.

**The event carries what you need.** Check the fields under EVENT KINDS. If the
question needs something no kind carries, the rule cannot be written yet, and
writing it anyway produces the silent no-op above.

If a rule fails any of these, say so rather than writing a weaker version of it.

## What makes a hook script correct

**Non-zero is refusal, and it is never consent.** This is the governing rule.
A hook that crashes, or is not executable, or whose interpreter is missing,
refuses. Do not add a fallback that exits 0 on an internal error — that converts
every bug in your script into permission.

**`set -e` will bite you.** Under errexit an ordinary "not found" from a
grep or a lookup aborts the script mid-decision, and the non-zero exit that
follows reads as a refusal you never decided to make. Prefer `set -uo pipefail`,
and handle the not-found case yourself.

**Read stdin exactly once.** The payload arrives on stdin and is consumed by the
first reader. Capture it into a variable, then extract from that variable.

**Let the matcher narrow.** If the hook re-checks whether the event concerns it,
that logic exists in two places and they will drift. Put the condition in the
matcher.

**Address the reason to the agent that has to act.** The refusal reaches the
agent whose action was blocked. Say what to do instead, not what went wrong. Do
not name the guardrail — the engine appends it.

## Prove it fires

Loading is not firing.

```
sloprail session start < /dev/null
```

is the load check. It catches an unknown event kind, a matcher naming a field
the kind does not carry, a hook that is not executable. It does not catch a rule
that is well-formed and wrong.

What it cannot catch, and you must verify by causing the event:

- a matcher that is valid but never true of anything real
- a mistyped key **inside** a list element — nothing checks those, and the
  expression evaluates to false forever
- a hook whose logic permits when it meant to refuse

So: cause the action the rule guards, and see the refusal. If you cannot make it
refuse, you have not written a working guardrail — you have written a file.

## Turning one off

Set `enabled: false`. Do not delete the folder.

The body holds the reasoning that produced the rule, which is exactly what
someone needs when deciding whether to switch it back on. A deleted guardrail
makes the next person rediscover both the rule and the argument against it. A
disabled one is inert and costs nothing to keep.

## Do not write these yet

Check `sloprail guardrail help` for what this build supports before relying on
either:

- **A rule that remembers across cycles.** `sloprail session state` resolves its
  scope from an environment the dispatcher does not yet set, so it fails from
  inside a hook. Such a rule loads, runs, and fails exactly when it needs its
  memory.
- **A rule bound to a `Post` kind.** The cycle-end hook point does not yet
  dispatch anything. The kinds are declared and bind without complaint, and
  nothing ever arrives.

Both produce the silent no-op. If a user asks for one, say it is not yet
supported rather than writing it.
