---
name: authoring-guardrails
description: Use when adding, fixing, or turning off a guardrail in a project that has sloprail installed — a rule under .sloprail/guardrails/ that refuses an agent's action. Covers the GUARDRAIL.md format, the hook contract, and how to prove a rule actually fires.
---

# Authoring Guardrails

## Get the event vocabulary from the load check

The kinds and their fields are per-build — they come from the modules compiled
into the engine — so they cannot be guessed and this skill deliberately does not
list them. A copy here would be the one you trust when the two disagree, and it
would be the stale one.

Ask the engine. Bind to a kind it does not have, and the load check answers with
every kind it does:

```
sr-session start < /dev/null
```

```
sloprail: guardrail "probe" not loaded:
  - event "NoSuchKind": no module produces it — this build has PreFileCreate, ...
```

Then bind to the kind you want with a deliberately wrong field name, and it
names that kind's real fields **with their types**:

```
  - event "PreFileCreate" binding 0: matcher "nope startsWith \"x/\"":
    unknown name nope (1:1) — PreFileCreate carries content (string), markers (list), path (string)
```

That is the same registry the engine enforces against, reported by the same
loader that will judge your rule, so it cannot drift from what the build does.

**Do this before writing, every time.** Two probes give you the kind list and
the fields of the kind you picked — which is everything a matcher reads.

## The failure this exists to prevent

A guardrail that never fires is worse than no guardrail. No guardrail is an
absence someone can notice. A rule that loads cleanly, sits in the project
looking enforced, and silently admits everything is a project believing it is
protected while it is not.

The work is not "write a plausible declaration" — it is "write one, then prove
it refused something."

## Where a guardrail lives

```
.sloprail/guardrails/<name>/GUARDRAIL.md
```

One folder per guardrail. **The folder name IS the guardrail's name** — it is not
repeated in the frontmatter, because a name recorded twice can disagree with
itself. The engine appends it to every refusal, so it is what a user sees when
the rule fires. Pick something that reads well there.

No naming rule is enforced; any directory name loads. Use kebab-case anyway, for
the reader.

Hook scripts sit **beside** the declaration in the same folder. A hook's command
is resolved relative to that folder, and the hook runs with it as its working
directory.

A judge hook's standard belongs in `RUBRIC.md` beside the declaration, which the
hook reads via `guardrailDir`. `GUARDRAIL.md`'s body is prose about the rule —
why it exists, what it decided — and nothing sends it to a model.

Nothing scaffolds this. Create the directories yourself.

## The declaration

YAML frontmatter, then a Markdown body. Both halves matter.

```markdown
---
enabled: true            # optional, defaults to true
hooks:                   # keyed by event kind
  PreFileCreate:
    - matcher: path startsWith "memories/"   # optional; absent means every occurrence
      hooks:
        - type: command
          command: ./check.sh
---

# Prose body: what the rule is and why it was written.
```

`hooks` is a map from **event kind** to a **list of bindings**. Each binding
narrows the event with an optional matcher and names the hooks to run when it is
admitted.

Two rules under one kind are two entries in that list. Writing a binding as a
bare mapping instead of a list item is refused at load —
`cannot unmarshal !!map into []guardrail.Binding` — so this mistake is loud
rather than silent. So is repeating a key: duplicates are reported by path and
line, at any depth, and the declaration does not load.

One guardrail may bind to several kinds and reuse one script across them.

A matcher is an expression over the event's **own fields**, and absent means
every occurrence. The operators, the two type groups, and why there is no glob:
[matchers.md](matchers.md).

Which kinds exist and what each one is for, per module — files
([file-event-hooks.md](file-event-hooks.md)), commands
([command-event-hooks.md](command-event-hooks.md)), the cycle itself
([cycle-event-hooks.md](cycle-event-hooks.md)). Read the one your rule is about.

A rule whose question spans more than one cycle — anything that records in one
place and judges in another — keeps what it knows in per-guardrail state, and
the scoping there is where such rules fail permissively:
[state-management.md](state-management.md).

## The hook contract

```yaml
hooks:
  - type: command
    command: ./check.sh some-argument
```

`type: command` is the only mechanism. The command runs through a shell, with
the guardrail's folder as its working directory.

**On stdin** the hook receives exactly one event as JSON — one, not a batch,
because deciding which of a batch a rule applied to is the matcher's work,
already done:

```json
{"event":{"kind":"PreFileCreate","fields":{"path":"x/a.md","content":"hi"}},
 "guardrailDir":"/abs/path/to/.sloprail/guardrails/<name>"}
```

The fields are the ones the load check names for that kind. Which guardrail
this is, is not carried — the engine ran the hook and already knows.

**What it writes back** is its exit status, and output explaining it:

- `exit 0` — permitted. Print nothing; silence is consent.
- non-zero — refused, whatever the hook wrote and whether or not it ran at all.
  An internal error is therefore a refusal, which is the safe direction: a
  broken script blocks rather than waves things through.

On a refusal the engine looks for the reason in this order:

1. `reason` from JSON on stdout — `{"decision":"block","reason":"..."}`
2. plain text on stdout
3. plain text on stderr — `echo "..." >&2; exit 1` is an ordinary way to refuse
   and is read as one
4. failing all that, a message naming the hook and its exit status

Write a reason anyway: only the hook knows what the agent should do instead.
Address it to the agent whose action was blocked, and say what to do rather than
what went wrong. The engine appends the guardrail's name, so the reason itself
is about the fix.

Hooks under one binding run in **declared order**, and the first refusal stops
the rest.

**`chmod +x` the script.** One that is not executable refuses every event it is
bound to, with a message saying so — loud rather than silent, but the rule is
not running until it is fixed. Session start warns about this at load.

Writing the script itself — the bash skeleton, why `set -e` is wrong here,
reading stdin once, asking about the transcript, and what a judge hook does
differently: [writing-a-hook-script.md](writing-a-hook-script.md).

## Is this rule worth writing

A guardrail earns its place when all of these hold.

**The violation is real and recurring.** You have seen it happen, or the user
named it. A rule against something nobody does costs every session and catches
nothing.

**A machine can tell.** "Writes under `memories/decisions/` without having loaded
`document-strategy`" is decidable. "The code is well designed" is not — unless
you hand a rubric to a judge hook, which is what `RUBRIC.md` is for.

**Refusing is the right response.** A `Pre` kind prevents the action; a `Post`
kind reports it after the fact and sends the agent round again. If the honest
response is neither — "note it and move on" — a guardrail is the wrong
instrument.

**The question is answerable from what the hook can reach.** That is more than
the event's own fields: also `sr-session state` for what earlier cycles recorded
([state-management.md](state-management.md)), `sr-session query` for the
transcript, and the tree itself. Both resolve their scope from the environment
the engine sets on a hook, so they answer inside one and decline outside it.
Check them before concluding a rule is unwritable. If the answer genuinely is
not reachable from any of them, say so — writing it anyway produces the silent
no-op.

If a rule fails any of these, say so rather than writing a weaker version.

## Prove it fires

Loading is not firing.

```
sr-session start < /dev/null
```

is the load check. It reports an unknown event kind, a matcher naming a field
the kind does not carry, a binding with no hooks, a duplicate key, and a hook
that cannot be run.

What survives it and still never fires:

- a matcher that is valid but true of nothing real
- a mistyped key **inside** a list element
- a hook whose logic permits where it meant to refuse

So cause the action the rule guards and see the refusal. If you cannot make it
refuse, you have not written a working guardrail — you have written a file.

## Turning one off

```yaml
---
enabled: false
hooks:
  ...
---
```

Set `enabled: false`, and keep the folder. The body holds the reasoning that
produced the rule, which is exactly what someone needs when deciding whether to
switch it back on; deleting it makes the next person rediscover both the rule
and the argument against it. A disabled one is inert — its kinds are not even
extracted — so it costs nothing to keep, and it is not validated, so it can be
parked half-written.
