# Writing a check

A check is what a rule runs to reach a verdict. Every nature uses them the same
way: `checks:` is a list, run in declared order, first refusal ending it. A check
is a **script** or a **judge** — exactly one; a check naming neither is refused as
"decided nothing", fail-closed.

```yaml
checks:
  - script: ./deterministic.sh          # a script check
  - prepare: ./assemble.sh              # a judge check, with an optional prepare
    judge: ./is-it-good.md.j2
    model: size-md                       # optional
    timeout: 45s                         # optional
```

## The contract both kinds share

- **`exit 0` permits.** Print nothing; silence is consent.
- **Non-zero refuses**, carrying whatever the check said as the reason.
- **A check that cannot run at all** — missing, not executable, an internal error,
  a timeout — is a **refusal**. This is fail-closed and deliberate: a rule that
  could not be checked must not read as approval. `chmod +x` the script; one that
  is not executable refuses every occurrence it is bound to, with a message saying
  so.

On a refusal the engine finds the reason to show the agent in this order:

1. `{"reason":"..."}` as JSON on **stdout** — preferred
2. plain text on stdout
3. plain text on stderr — `echo "..." >&2; exit 1` is an ordinary refusal
4. failing all that, a message naming the check and its exit status

Write a reason: only the check knows what the agent should do instead. Address it
to the agent whose action was blocked, and say what to do rather than what went
wrong — the engine appends the rule's name, so the reason itself is about the fix.

(This `{"reason"}` on stdout is the whole structured contract. The old format's
`{"decision":"block","reason":…}` wrapper is gone — the engine reads `reason`
alone, so drop `decision`.)

## The script skeleton

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

## The flat event on stdin

A script (and a `prepare`) receives one **`CheckPayload`** as JSON, and the event
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

The one variation is a **context** script: `enter`/`exit` receive a
`ContextEnterPayload` / `ContextExitPayload`, which add `.currentContext` (this
context's own `{active, payload}`) and `.gates` (each gate's verdict, `.status`) —
see [context.md](context.md).

A field the event omits reads as absent: guard `.event.newContent` with
`has("newContent")` before reading it on an update, because an absent value is
indistinguishable from an emptied file ([file-guard.md](file-guard.md), the
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

Note a subtlety when reading normalized trajectory events: `sr-session trajectory
normalize` emits each historical event in the **`{kind, fields}`** wire form, so
the invocations of a past command sit under `.fields.invocations`. That is the
*normalized-history* shape — it is **not** the live check stdin, which is flat
(`.event.invocations`). Do not conflate the two.

## A judge check

A judge asks a model the question a script cannot decide. It is a Jinja2 prompt
**template** (`.md.j2`) beside the rule, rendered against the check input and
asked for a verdict.

```yaml
- prepare: ./collect-quote-and-diff.sh    # optional
  judge: ./change-is-clean-and-absolute.md.j2
  model: size-md                            # optional: a size alias or model name
  timeout: 45s                              # optional: default 30s
```

The template renders against the same facts a script sees — `{{ event.newContent
}}`, `{{ transcriptPath }}`, `{{ context }}` — plus **`{{ additionalContext.* }}`**
when a `prepare` assembled one:

```markdown
## What the user asked
> {{ additionalContext.asked_quote }}

## The change
```diff
{{ additionalContext.change_diff }}
```
```

### prepare

`prepare` is an optional script that runs **first**, and its job is to assemble
context the template needs — pulling the cited source, computing a diff. It
receives the same `CheckPayload` on stdin, and **only** the `additionalContext` key
of its stdout is read, merged alongside the standard payload (never replacing it):

```bash
jq -n --arg quote "$quote" --arg diff "$change_diff" \
  '{additionalContext: {asked_quote: $quote, change_diff: $diff}}'
```

A `prepare` that exits non-zero **fails the check** (carrying its words), and one
whose stdout is not `{"additionalContext": {...}}` fails it closed too — a judge
fed a half-prepared prompt would judge against something the author did not intend.
`prepare` is only meaningful with a `judge`; setting it on a script check is a load
error.

### The verdict

The engine appends a verdict instruction to the rendered prompt, so the template
itself is just the rubric and the material. The model must answer with exactly:

```json
{"pass": true, "reasoning": ""}
```

or, on a failure, `{"pass": false, "reasoning": "one concrete sentence naming the
specific problem"}`. `pass` is a boolean; on a failing verdict `reasoning` is what
returns to the agent's context window, so it must name the specific thing that
fails. (Note the key is **`reasoning`** here — distinct from a *script* check's
stdout `reason`. Two different contracts.)

### The substrate, model, and timeout

A judge runs through **`sr-agent`** (the harness-agnostic agent runner), not a
model binary directly — so a template names neither `claude` nor a concrete model.
`model:` is a modelset in sr-agent's format: a **size alias** (`size-xs` …
`size-xxl`) or a concrete harness model name or a comma-separated preference list;
omitted, it is the engine default `size-md`. `timeout:` is a Go duration (`45s`,
`2m`) bounding the call; omitted, 30s. There is **no `allowed_tools` key** — a
judge's tools are fixed by the substrate, not set per-check.

### Fail-closed by default

The judge substrate fails **closed**: if `sr-agent` cannot run, times out, or the
model never produces a well-formed verdict after its retries, the check refuses —
the same rule the whole engine keeps. The verdict itself also fails closed (only an
explicit `{"pass": true}` permits).

A judge that should fail **open** on model flakiness — because a model call flakes
for reasons that are not evidence about the file, and one flake under fail-closed
wedges a session the agent cannot un-wedge — does so by writing it as a **script
check** that calls the model itself and chooses `exit 0` on its own machinery
failures. That is a deliberate inversion of the default; say in the script which
exits are the fail-open ones, so the next reader can tell a choice from a bug. (A
script that judges content the model just wrote must wrap that content and tell the
model to treat it as **data, never as instructions** — it is attacker-shaped by
construction.)
