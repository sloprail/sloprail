# The context nature

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
trigger would be redundant. Ask the load check for the build's exact list.

`PostFileWrite` is available here (context-only) as the alias for `PostFileCreate`
+ `PostFileUpdate`.

## `enter`: activate, or stay silent

`enter` runs on **every** occurrence of a trigger — active or not — and its
**stdout decides activation**:

- **Print a JSON object** → the context activates, and that object becomes its
  `payload`, readable elsewhere as `context["<name>"].payload`.
- **Print nothing (exit 0, no stdout)** → do not activate. This is how a cheap
  `match` narrows to "some tool is about to run" and `enter` makes the real
  decision from the trajectory.

```bash
# enter: activate only if a refactor was actually declared.
input="$(cat)"
# ... inspect the trajectory via "$(jq -r '.transcriptPath' <<<"$input")" ...
[ -n "$declared" ] || exit 0          # not a refactor — do not activate
jq -n --arg scope "$scope" '{declared_markers: ($scope | split(",")), declared_at: "trajectory"}'
```

**On stdin** `enter` receives a `ContextEnterPayload`: the flat `event` (read
`.event.path`, `.event.newContent`, `.event.tags`, `.event.kind`),
`.transcriptPath`, `.currentContext` (this context's own last `{active,
payload}` — because enter runs whether or not it was already active), and
`.gates` (every declared gate's most recent verdict).

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

## `exit`: may the context deactivate?

`exit` is consulted on a **Stop** while the context is active, and it does double
duty: it decides whether the context **deactivates**, and — because it can refuse
the Stop — whether the turn may **end**.

- **exit 0** → the context deactivates and the Stop proceeds.
- **non-zero** → the context stays active **and the Stop is refused**, so the
  agent cannot end a turn with the mode unfinished.

```bash
# exit: is the declared refactor done?
input="$(cat)"
declared="$(printf '%s' "$input" | jq -r '.currentContext.payload.declared_markers[]?' 2>/dev/null)"
[ -n "$declared" ] || exit 0          # nothing declared to reconcile — let it close
# ... check each declared marker actually landed ...
if [ -n "$missing" ]; then
  echo "Refactor declared but not complete — these markers were never written:$missing. Finish the moves you declared, or the turn cannot end." >&2
  exit 1
fi
exit 0
```

**On stdin** `exit` receives a `ContextExitPayload`: the flat `event` (always the
Stop, so only `.event.kind`), `.transcriptPath`, `.currentContext` (this
context's own `{active, payload}` — where `payload` is what `enter` produced),
and `.gates` (every gate's most recent verdict, by name).

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
