# Context

A context is an **activatable scope**. It is not a check that refuses — it is a
*mode* that turns on and off as the session goes, accumulates what it sees while
on, and exists so that **other rules can depend on it**: a gate `require`s it, or
a match reads its `active` flag and `payload`.

```
.sloprail/context/<name>/context.yaml
```

```yaml
# research-run — a #research tag was written this turn, so this branch is
# research. Wakes on the tag event, filtered to #research, and activates.
on:
  - event: PostTagWrite
    match: any(event.tags, .label == "research")
enter: ./enter.sh
exit: ./exit.sh
```

Three keys. `on` is the list of triggers that wake `enter` (each an `event` and
an optional `match` — the same nested `event.*` scope a gate uses). `enter` is
the script that decides whether to activate. `exit` is the script consulted on
Stop while active, deciding whether the context may deactivate. A context has no
`checks` — its verdict-bearing work is done by the gates that require it.

## What a context triggers on

Any **pre-action** kind (so a refactor can be spotted before a write via
`PreToolUse`), plus the **`PostFile*` and `PostTagWrite`** kinds (so it can wake
on a tag the agent just wrote, or a file that just landed). A context may **not**
trigger on `Stop` — its `exit` is *always* checked on Stop anyway, so a Stop
trigger would be redundant. Ask the load check for the build's exact list, or see
the per-nature admission table in [events.md](events.md).

`PostFileWrite` is available here (context-only) as the alias for `PostFileCreate`
+ `PostFileUpdate`.

## `enter`: activate, or decline

`enter` runs on **every** occurrence of a trigger — active or not — and its
**exit code decides activation**; its stdout only decides the payload:

- **Exit 0 and print a JSON object** → the context activates (or stays active),
  and that object **replaces** its `payload`, readable elsewhere as
  `context["<name>"].payload`.
- **Exit 0 and print nothing** → the context **still activates** (or stays
  active), keeping the payload it already had — none, the first time. A silent
  clean exit is not a "no".
- **Exit non-zero** → **decline**: this trigger leaves the context exactly as it
  was. An inactive context stays inactive; an active one stays active with its
  payload (declining is not `exit` saying done).

So every "no" path in an `enter` must exit non-zero. This is how a cheap `match`
narrows to "some tool is about to run" and `enter` makes the real decision from
the trajectory.

```bash
# enter: activate only if a refactor was actually declared.
input="$(cat)"
# ... inspect the trajectory via "$(jq -r '.transcriptPath' <<<"$input")" ...
[ -n "$declared" ] || exit 1          # not a refactor — decline (exit 0 would activate)
jq -n --arg scope "$scope" '{declared_markers: ($scope | split(",")), declared_at: "trajectory"}'
```

Output that is not a flat JSON object (an array, a string, invalid JSON) is not
read as a payload: the engine reports it and leaves the context as it was.

**On stdin** `enter` receives a **ContextEnterPayload**: the flat `event` (read
`.event.path`, `.event.newContent`, `.event.tags`, `.event.kind`),
`.transcriptPath`, `.currentContext` (this context's own last `{active, payload}`
— because enter runs whether or not it was already active), and `.gates` (every
declared gate's most recent verdict). Its full shape and the other payload
envelopes are in [events.md](events.md).

When a trigger's `match` already settled the condition (e.g. `any(event.tags,
.label == "research")`), `enter` need not re-check it — activating unconditionally
is fine:

```bash
jq -n '{declared: true}'
```

### Accumulating into a registry

A context that must remember **several** things across a turn — every tag
declared, every artifact that landed — logs each into `sr-session state` under
its own name, rather than growing `payload` in place (payload is one object,
replaced on each enter). One entry per subject:

```bash
kind="$(printf '%s' "$input" | jq -r '.event.kind // ""')"
case "$kind" in
  PostTagWrite)
    printf '%s' "$input" | jq -r '.event.tags[]?.label // empty' | while IFS= read -r tag; do
      [ -n "$tag" ] && sr-session state set "tag:$tag" "declared"
    done ;;
  PostFileCreate|PostFileUpdate)
    path="$(printf '%s' "$input" | jq -r '.event.path // ""')"
    [ -n "$path" ] && sr-session state set "artifact:$path" "$kind" ;;
esac
jq -n '{active_since: "trajectory"}'
```

A paired gate then reads that registry back with `sr-session state list --owner
<this-context>` — the one read that crosses the per-guardrail boundary. See
[state-management.md](state-management.md).

## An `enter` that cannot run refuses its trigger, it is not a decline

A non-zero exit is a decline, and a decline is quiet. A script that **cannot
run at all** (missing, not executable, no `#!` line, a bad interpreter, killed
on the timeout) is a different thing and is never read as a decline: a context
left off by a broken `enter` would silently disarm every rule that reads it
(`context.<name>.active` in a match, a file-guard narrowed by it), although the
mode was meant to be on. So:

- on a **Pre\*** trigger the event is **refused** (a PreToolUse deny, the same
  as a gate's), with a reason naming the context, the script and the fix
  (`chmod +x`, or add `#!/usr/bin/env bash`): the context could not be entered,
  so what it guards cannot be judged;
- a **Post\*** trigger (PostFile\*, PostTagWrite) cannot deny, since the work is
  done; it is handled at the **Stop**, which is refused with the same reason;
- a Stop is also refused while a declared `enter` **or** `exit` cannot run,
  even for a context that never triggered, until the script is fixed. (An
  `exit` that cannot run keeps the context active, the guarding direction; the
  Stop refusal makes the fault visible.)
- an `enter` that runs but prints something that is not a flat JSON object is
  refused the same way.

The repair of the context's own script is never refused, so a context that
triggers on every tool call cannot lock the agent out of fixing it: a Write or
Edit of a file inside the context's folder, and a command made only of `chmod`
(an add-execute mode such as `+x` or `755`) or `sr-file edit|write <path>` on
paths inside that folder, go through. Anything else in the same call, or any
other command, is still refused until the script is fixed. `sr-file delete`,
`rm` and `mv` of the script are not exempt, nor is a `chmod` that removes the
bit: only an Edit or Write of it, and `chmod +x` or `sr-file edit|write` on it.

A script that runs and exits non-zero is still a decline, and refuses nothing.

## `exit`: may the context deactivate?

`exit` is consulted on a **Stop** while the context is active, and it decides one
thing: whether the context **deactivates**. It never refuses the Stop — a context
is a mode, not a check. To keep the turn from ending while the mode is
unfinished, a **gate** on `Stop` that reads the context does the refusing (see
"A thin exit that reads a gate's verdict" below).

- **exit 0** → the context deactivates.
- **non-zero** → the context stays active for the next cycle. The Stop still
  proceeds unless a gate refuses it.

```bash
# exit: is the declared refactor done?
input="$(cat)"
declared="$(printf '%s' "$input" | jq -r '.currentContext.payload.declared_markers[]?' 2>/dev/null)"
[ -n "$declared" ] || exit 0          # nothing declared to reconcile — let it close
# ... check each declared marker actually landed ...
[ -z "$missing" ] || exit 1           # not done — stay active (a gate refuses the Stop)
exit 0
```

**On stdin** `exit` receives a **ContextExitPayload**: the flat `event` (always the
Stop, so only `.event.kind`), `.transcriptPath`, `.currentContext` (this context's
own `{active, payload}` — where `payload` is what `enter` produced), and `.gates`
(every gate's most recent verdict, by name). Full shape in [events.md](events.md).

### A thin exit that reads a gate's verdict

Often the real judgement lives in a gate, and `exit` only mirrors its verdict —
symmetric to how a gate's `match` reads `context`. `exit` reads `.gates["<gate>"]`
and its `.status`:

```bash
input="$(cat)"
status="$(printf '%s' "$input" | jq -r '.gates["depth-check"].status // "fail"' 2>/dev/null)"
[ "$status" = "pass" ] && exit 0
exit 1
```

`.gates[...].status` is `"pass"`/`"fail"`. This keeps the depth logic in one
place (the gate) and lets the context's `exit` be a one-line consequence of it.

## How other rules use a context

This is the point of a context — what it is *for*.

- **A gate `require`s it.** `require: [{context: research-run}]` holds the gate's
  check until the context has entered **this cycle**, so anything the context
  accumulated is current when the gate reads it.
- **A match reads its state.** In a gate's or another context's `match`,
  `context["research-run"].active` (bool) and `context["research-run"].payload`
  (the object `enter` produced) are readable. `context["tag-declared"].active`
  skips a gate declaratively when the context never activated; `not
  context["tag-declared"].active` is exactly "no tag was declared this cycle".

The `active` read in a `match` is only *meaningful* once the context has actually
run its `enter` this cycle — which is why a gate that acts on it also `require`s
it, to guarantee that ordering. (A `match` that merely *reports* absence — `not
…active` — needs no `require`; there is nothing for the context to have run
first.)

### The Stop order

At Stop the work runs in one fixed order: **context enters → commit-required →
tracked-range verify → gates → context exits.** Enters come first, so
commit-required, the verify of the session's tracked ranges (no model; see
[file-guard.md](file-guard.md#where-it-is-enforced)) and a gate's `match`/`require`
all read the contexts this turn entered, not last turn's state. Exits come last, so
a context that closes at this Stop is still active for every rule checked at that
same Stop, and is closed afterwards. A file-guard is not judged at Stop: `sr-checks
run` judges it, and it reads no context (a file-guard's match cannot name one). (Before a tool call the order is enters, then structure
gates, then gates.)

Note that a `match` reads `context[...]` at **run time** (context names are
project-defined, so the type checker leaves the map open) — a typo in the context
name is not caught at load. Cause the trigger and confirm the dependent rule
actually fires.

## Turning one off

Keep the folder; disable the context from `.sloprail/config.yaml` by its
qualified name — remembering that any gate which `require`s it will then refuse
(its precondition can never be met), so disable the dependents too if that is not
what you want:

```yaml
disabled:
  - <plugin-or-project>/context/<name>
```

The nature is part of the key — `.../context/<name>` — because a context and a
gate may share a bare name.
