---
name: authoring-guardrails
description: Use when adding, fixing, or turning off a guardrail in a project that has sloprail installed — a rule under .sloprail/guardrails/ that refuses an agent's action. Covers the GUARDRAIL.md format, matchers, the hook contract, and how to prove a rule actually fires.
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

Everything else — the format below — is not derivable this way, so it lives
here.

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

# Prose body: what the rule is, and the rubric a judge hook reads.
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

## The body is the judge's rubric

The prose under the frontmatter is not documentation sitting next to the rule.
A judge hook reads it as its rubric — the criteria a model is asked to judge
against. Write it as the standard being applied, not as a note about it: say
what passes and what fails, in the words you want a judge to weigh.

It reaches the hook byte for byte; nothing normalises it. A hook reads it via
`guardrailDir` on stdin.

## Matchers

An expression over the event's **own fields** — the ones the load check names
for that kind, and nothing else. No filesystem, no environment, no other events.
It must evaluate to a boolean. Absent means every occurrence.

A **misspelled top-level field is caught at load**: the binding is refused by
name and session start prints what the kind does carry. You need not defend
against that one.

Operators on a field of type `string`:

| | |
|---|---|
| `startsWith` | `path startsWith "memories/"` |
| `endsWith` | `path endsWith ".md"` |
| `contains` | `path contains "/decisions/"` |
| `matches` | `path matches "^docs/[0-9]+-"` (regular expression) |
| `&&` `\|\|` `!` | `path startsWith "src/" && !(path endsWith "_test.go")` |
| `==` `!=` | `path == "README.md"` |

A field of type `list` is read with `any`, `all`, `none` and `len`:

```
any(invocations, .bin == "curl")
!any(invocations, .bin == "npm")
len(invocations) > 1
```

Check the type printed beside each field — the two groups are not
interchangeable.

**The inside of a list is not checked.** No module declares what its list field
holds, so a key read off an element is verified against nothing: a mistyped one
compiles, loads, and evaluates to false forever. Unlike a top-level typo,
nothing catches it. Confirm a list matcher by causing the event.

### There is no glob

Not an omission. A glob's semantics differ between tools enough that a
familiar-looking pattern would be familiar and subtly wrong — whether `*`
crosses a directory separator is not the same answer in two places. Write what
you mean:

| want | write |
|---|---|
| everything under `memories/` | `path startsWith "memories/"` |
| every markdown file | `path endsWith ".md"` |
| markdown under `memories/` | `path startsWith "memories/" && path endsWith ".md"` |
| a real pattern | `path matches "^memories/[0-9]{8}_"` |

Narrowing is the matcher's job, not the hook's. A hook that re-checks whether
the event concerns it re-implements its own binding, and the two will drift.

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
- non-zero — refused.

**A non-zero exit is never consent.** It refuses whatever the hook did or did not
write, and whether or not it managed to run at all. Do not add a fallback that
exits 0 on an internal error — that turns every bug in your script into
permission.

On a refusal the engine looks for the reason in this order:

1. `reason` from JSON on stdout — `{"decision":"block","reason":"..."}`
2. plain text on stdout
3. plain text on stderr — `echo "..." >&2; exit 1` is an ordinary way to refuse
   and is read as one
4. failing all that, a message naming the hook and its exit status

Write a reason anyway: only the hook knows what the agent should do instead.
Address it to the agent whose action was blocked, say what to do rather than
what went wrong, and **do not name the guardrail** — the engine appends it.

Hooks under one binding run in **declared order**, and the first refusal stops
the rest.

**`chmod +x` the script.** One that is not executable refuses every event it is
bound to, with a message saying so — loud rather than silent, but the rule is
not running until it is fixed. Session start warns about this at load.

### Writing the script

```bash
#!/usr/bin/env bash
set -uo pipefail
payload="$(cat)"                                   # stdin, read ONCE
path="$(printf '%s' "$payload" | jq -r '.event.fields.path')"
# ... decide ...
echo '{"decision":"block","reason":"Writing '"$path"' requires ..."}'
exit 1
```

`set -uo pipefail`, **not** `set -e`. Under errexit an ordinary non-zero from a
grep or a lookup aborts the script mid-decision, and the exit status that
follows is read as a refusal the rule never decided to make.

**Read stdin exactly once.** It is consumed by the first reader. Capture it into
a variable, then extract from that variable.

## Asking what the agent did

Some rules ask about the conversation rather than the pending action ("was this
skill loaded before the write?").

```bash
payload="$(cat)"
printf '%s' "$payload" | sr-session query --where 'type == "assistant"'
```

It needs the harness payload on its **own** stdin — the same JSON the hook was
handed, which carries the transcript path. `--where` is the same expression
language a matcher uses, but over an **entry's** fields, which are not the event
fields: `type`, `uuid`, `parentUuid`, `logicalParentUuid`, `timestamp`,
`isSidechain`, `message`, `toolUseResult`. Sub-agent entries are excluded unless
`--include-sidechains`.

It reads the whole session, not just the part not yet judged.

## Is this rule worth writing

A guardrail earns its place when all of these hold.

**The violation is real and recurring.** You have seen it happen, or the user
named it. A rule against something nobody does costs every session and catches
nothing.

**A machine can tell.** "Writes under `memories/decisions/` without having loaded
`document-strategy`" is decidable. "The code is well designed" is not — unless
you hand the body to a judge, which is what the prose is for.

**Refusing is the right response.** A `Pre` kind prevents the action; a `Post`
kind reports it after the fact and sends the agent round again. If the honest
response is neither — "note it and move on" — a guardrail is the wrong
instrument.

**The question is answerable from what the hook can reach.** That is more than
the event's own fields: also `sr-session state` for what earlier cycles
recorded, `sr-session query` for the transcript, and the tree itself. Check them
before concluding a rule is unwritable. If the answer genuinely is not reachable
from any of them, say so — writing it anyway produces the silent no-op.

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

Set `enabled: false`. **Do not delete the folder.** The body holds the reasoning
that produced the rule, which is exactly what someone needs when deciding
whether to switch it back on. A deleted guardrail makes the next person
rediscover both the rule and the argument against it. A disabled one is inert —
its kinds are not even extracted — so it costs nothing to keep. A disabled
declaration is also not validated, so it can be parked half-written.

## Remembering across cycles

`sr-session state get|set|list` is a per-guardrail key-value store that survives
between cycles of one session. It resolves its own scope from `SR_GUARDRAIL`,
`SR_SESSION_ID` and `SR_WORKSPACE`, all three set by the engine on every hook it
runs — so a hook calls it with no arguments beyond the key.

```sh
prev=$(sr-session state get seen 2>/dev/null || echo 0)
sr-session state set seen "$((prev + 1))"
```

Scoped to the guardrail, so two rules cannot collide on a key name, and to the
session, so one session's memory is not another's. Outside a hook there is no
guardrail in scope and it says so rather than guessing.

## Post kinds

The kinds whose names begin `Post`, and the one about the cycle itself, are
dispatched at the end of a cycle from the `Stop` and `SubagentStop` hook points.
They arrive with the cycle's actual changes, established by diffing the tree
against the baseline taken at `SessionStart`. The load check names them among
the kinds this build produces; they are not restated here, because a copy of
that list is what an author would trust after a module is added and it is the
copy that goes stale.

The difference from a `Pre` kind is what a refusal means. A `Pre` kind runs
before the action and prevents it. A `Post` kind runs after, so the change is
already on disk — refusing does not undo it, it tells the agent the cycle is not
finished and it must fix what it did. That makes `Post` the right kind for a
rule about the *result* of a turn ("every new file under `memories/` has
frontmatter") and the wrong one for a rule about permission to act at all.

The cycle kind carries no fields at all. It fires once per cycle regardless of
what changed, which is what a rule about the turn as a whole wants — but it
means such a rule has to establish its own subject, usually by asking
`sr-session query` about the transcript, or by having per-file rules record
what they saw into `sr-session state` for it to read.

A `Post` refusal is reported to the agent as a blocking error on the cycle, and
the cycle's read mark does not advance — so the next `Stop` judges the same span
again, and a rule that stays unsatisfied stays reported rather than scrolling
away. Revalidation keeps this from re-judging content that has not changed: a
file already judged against the same fingerprint is skipped.
