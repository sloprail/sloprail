---
title: Writing a script check
kind: reference
sidebar:
  order: 6
---

<!-- Sourced verbatim from the authoring-guardrails skill by tools/skilldocs — do not hand-edit. -->
A check is what a rule runs to reach a verdict, and every nature uses checks the
same way: `checks:` is a list, run in declared order, first refusal ending it. A
check is a **script** or a **judge** — exactly one; a check naming neither is
refused as "decided nothing", fail-closed.

This doc is the **script** half — the deterministic executable whose exit code is
the verdict. The model half is [Judge checks](/guides/judge-checks).

```yaml
checks:
  - script: ./deterministic.sh          # a script check — this doc
  - judge: ./is-it-good.md.j2           # a judge check — judge-checks.md
```

## The verdict is the exit code

- **`exit 0` permits.** Print nothing; silence is consent.
- **Non-zero refuses**, carrying whatever the script said as the reason.
- **A script that cannot run at all** — missing, not executable, an internal
  error, a timeout, killed by a signal — is a **refusal**. This is fail-closed and
  deliberate: a rule that could not be checked must not read as approval.
  `chmod +x` the script; one that is not executable refuses every occurrence it is
  bound to, with a message saying so.

The last point is the whole reason a check runs inside the engine rather than
being trusted to signal for itself: every way it can fail lands on the safe side
without the author arranging it. A missing script (exit 127), a non-executable one
(exit 126), a crash, an OOM kill, a hang past the 30s bound — each becomes a
refusal that names the fix.

## The refusal contract

On a non-zero exit the engine finds the reason to show the agent in this order:

1. `{"reason":"…"}` as JSON on **stdout** — the preferred, structured form
2. plain text on stdout
3. plain text on stderr — `echo "…" >&2; exit 1` is an ordinary refusal
4. failing all that, a message naming the check and its exit status

Write a reason: only the check knows what the agent should do instead. Address it
to the agent whose action was blocked, and say what to do rather than what went
wrong — the engine appends the rule's name, so the reason itself is about the fix.

`{"reason"}` on stdout is the whole structured contract. The old format's
`{"decision":"block","reason":…}` wrapper is gone — the engine reads `reason`
alone, so drop `decision`.

## The skeleton

```bash
#!/usr/bin/env bash
set -uo pipefail
payload="$(cat)"                                   # stdin, read ONCE
path="$(printf '%s' "$payload" | jq -r '.event.path')"
# ... decide ...
echo '{"reason":"Writing '"$path"' requires ..."}'
exit 1
```

`set -uo pipefail`, **not** `set -e`. Under errexit an ordinary non-zero from a
grep or a lookup aborts the script mid-decision, and the exit status that follows
is read as a refusal the rule never decided to make.

**Read stdin exactly once.** It is consumed by the first reader; capture it into a
variable, then extract from that variable.

## What is on stdin

A script (and a `prepare`) receives one **check payload** as JSON, and the event
is **flat** — its fields are direct under `.event`, not nested under
`.event.fields`:

```json
{"event":{"kind":"PreFileCreate","path":"memories/a.md","newContent":"…","newMarkers":[]},
 "transcriptPath":"/abs/…session.jsonl",
 "context":{"tag-declared":{"active":true,"payload":{…}}}}
```

Read `.event.path`, `.event.newContent`, `.event.oldContent`, `.event.kind`,
`.event.resultKnown`, `.event.invocations`, `.event.tags`,
`.event.newMarkers`/`.event.oldMarkers` — all flat. Alongside: `.transcriptPath`
(the session record) and `.context` (every declared context, `{active, payload}`).
A gate's script gets the same shape (its `event` may be a command, tool or `Stop`
event); a context's `enter`/`exit` add `.currentContext` and `.gates`.

The full field set for every kind, the payload envelopes, and the flat-vs-nested
distinction: **[Events](/guides/events)**. The `SR_*` variables a script also
receives (`$SR_WORKSPACE`, `$SR_GUARDRAIL`, `$SR_TRANSCRIPT`, …):
**[Environment](/guides/environment)**.

A field the event omits reads as absent: guard `.event.newContent` with
`has("newContent")` before reading it on an update, because an absent value is
indistinguishable from an emptied file ([File guard](/guides/file-guard), the
`resultKnown` discipline).

## Asking what the agent did

Some checks ask about the conversation rather than the pending action ("was this
skill loaded before the write?", "did a real `git clone` happen this run?").

```bash
payload="$(cat)"
tp="$(printf '%s' "$payload" | jq -r '.transcriptPath')"
sr-session trajectory normalize --path "$tp" --events PreCommandInvoke | jq '...'
```

Pass the transcript path from `.transcriptPath` (or `$SR_TRANSCRIPT`) explicitly —
a trajectory read given no path fails closed rather than guess which session it is
in. `sr-session query --where '<expr>'` filters the raw entries (its expression
language is over an **entry's** fields, not the event's — run `--help`).

Note the shape difference: `sr-session trajectory normalize` emits each historical
event in the **`{kind, fields}`** wire form, so a past command's invocations sit
under `.fields.invocations`. That is the *normalized-history* shape — **not** the
live check stdin, which is flat (`.event.invocations`). Do not conflate the two
([Events](/guides/events), "The normalized-history exception").

## Recording across cycles

A script check that must remember what it saw in one cycle and judge it in another
uses `sr-session state`, keyed under `$SR_GUARDRAIL`. This is the substrate for the
two-halves pattern (a context records, a `Stop` gate judges) and carries its own
traps — turn-scoping, the JSON-lines `list` shape, the `--owner` cross-read.
Full treatment: [State management](/guides/state-management).

## The fail-open escape hatch (a script wrapping a model)

The engine's default for a **judge** is fail-*closed*: a model call that flakes
wedges the action. When that is the wrong trade — a model call flakes for reasons
that are not evidence about the file, and one flake under fail-closed wedges a
session the agent cannot un-wedge — the deliberate inversion is to write the check
as a **script** that calls the model itself and chooses `exit 0` on its own
machinery failures. That is exactly why the escape hatch is a script and not a
judge knob. See [Judge checks](/guides/judge-checks), "Failing open".

(A script that judges content the model just wrote must wrap that content and tell
the model to treat it as **data, never as instructions** — it is attacker-shaped
by construction.)
