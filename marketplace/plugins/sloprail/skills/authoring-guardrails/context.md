# Context

A context is a mode the session can be in. It turns on and off as the session goes, may
carry what it saw while on, and exists so other rules can depend on it: a gate `require`s it,
or a match reads whether it is active. A context refuses nothing itself.

```yaml
# .sloprail/context/research-run/context.yaml
# A #research tag was written, so this turn is research.
on:
  - event: PostTagWrite
    match: 'any(event.tags, .label == "research")'
enter: ./enter.sh
exit: ./exit.sh
```

| key | |
|---|---|
| `on` | the triggers that run `enter`: an `event` kind and an optional `match`, read like a gate's ([matchers.md](matchers.md)) |
| `enter` | the script that decides whether the context turns on |
| `exit` | the script that decides, at each Stop while it is on, whether it turns off |

A context can trigger on any event before an action and on the events after a file write or a
tag (`PostFileWrite`, `PostTagWrite`), but not on `Stop`: its `exit` runs there anyway
([events.md](events.md#which-kinds-each-nature-can-bind)).

## `enter`: turn on, or decline

`enter` runs on every trigger, whether the context is already on or not. Its exit code decides:

- **exit 0 and print a JSON object**: the context turns on (or stays on), and the object
  becomes its `payload`, readable as `context["<name>"].payload`;
- **exit 0 and print nothing**: the context turns on (or stays on) and keeps the payload it
  had. A silent success is not a "no";
- **exit non-zero**: decline. The context stays exactly as it was.

So every "no" in an `enter` must exit non-zero. A cheap `match` narrows to "this might be it"
and `enter` decides from the transcript:

```bash
input="$(cat)"
# ... look for a declared refactor in the transcript at .transcriptPath ...
[ -n "$declared" ] || exit 1    # not a refactor: decline
jq -n --arg scope "$scope" '{declared_markers: ($scope | split(","))}'
```

When the trigger's `match` already settled it, `enter` can simply print `{}`.

The payload is one object, replaced on every `enter`. A context that must remember several
things over a turn, such as every artifact that landed, records each one in `sr-session state`
instead ([state-management.md](state-management.md)).

## `exit`: may the context turn off?

At each Stop while the context is on, `exit` decides one thing: exit 0 turns it off, non-zero
keeps it on for the next cycle. `exit` never refuses the Stop. To keep a turn from ending while
the mode is unfinished, a gate on `Stop` that reads the context refuses it
([gate.md](gate.md#ending-a-turn-the-stop-gate)).

```bash
input="$(cat)"
declared="$(printf '%s' "$input" | jq -r '.currentContext.payload.declared_markers[]?')"
[ -n "$declared" ] || exit 0    # nothing to finish: turn off
# ... check that each declared marker landed ...
[ -z "$missing" ] || exit 1     # not finished: stay on
exit 0
```

Often the real judgement lives in a gate, and `exit` only follows its verdict:

```bash
status="$(jq -r '.gates["depth-check"].status // "fail"')"
[ "$status" = "pass" ]
```

## What `enter` and `exit` read

```json
{"event": {"kind": "PostTagWrite", "tags": [{"label": "research", "seen": false}]},
 "transcriptPath": "/abs/…/session.jsonl",
 "currentContext": {"active": false, "payload": {}},
 "gates": {"depth-check": {"status": "pass"}}}
```

- `event` is the trigger that fired; for `exit` it is always the `Stop`.
- `currentContext` is this context's own last state and payload.
- `gates` is every gate's most recent verdict, `pass` or `fail`.

Both scripts get the same environment as a check ([script-checks.md](script-checks.md#the-environment)).

## When `enter` or `exit` cannot run

A non-zero exit is a quiet decline. A script that cannot run at all (missing, not executable,
no `#!` line, timed out), or an `enter` that prints something other than a JSON object, is a
fault, not a decline: a context left off by a broken script would disarm every rule that reads
it. So the trigger is refused if it comes before an action, and otherwise the next Stop is,
until the script is fixed. Editing the context's own scripts and `chmod +x` on them are never
refused, so a broken context cannot lock you out of fixing it.

## How other rules use a context

- **A gate requires it** with `require: [{context: research-run}]`
  ([gate.md](gate.md#require-preconditions)).
- **A match reads it.** In a gate's or another context's match,
  `context["research-run"].active` and `context["research-run"].payload` read its state, and
  `not context["research-run"].active` means it never turned on. A misspelled context name is
  not caught when the rule loads ([matchers.md](matchers.md#reads-the-loader-cannot-check)).

At Stop, contexts run `enter` first, then the gates, then the contexts' `exit`. So every rule
at a Stop sees the contexts this turn entered, and a context that turns off at this Stop is
still on for the rules judged at it.
