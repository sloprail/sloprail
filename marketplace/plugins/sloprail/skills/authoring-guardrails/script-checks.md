# Writing a script check

A check is what a rule runs to reach a verdict, and every nature uses checks the
same way: `checks:` is a list, run in declared order, first refusal ending it. A
check is a **script** or a **judge** — exactly one; a check naming neither is
refused as "decided nothing", fail-closed.

This doc is the **script** half — the deterministic executable whose exit code is
the verdict. The model half is [judge-checks.md](judge-checks.md).

```yaml
checks:
  - script: ./deterministic.sh          # a script check — this doc
  - judge: ./is-it-good.md.j2           # a judge check — judge-checks.md
```

## The verdict is the exit code

- **`exit 0` permits.** Print nothing; silence is consent.
- **Non-zero refuses**, carrying whatever the script said as the reason.
- **A script that cannot run at all** — missing, an internal error, a timeout,
  killed by a signal — is a **refusal**. This is fail-closed and deliberate: a
  rule that could not be checked must not read as approval. (A script without
  the execute bit is not in this list: the engine runs it through its `#!`
  interpreter, or `sh`, so no `chmod` is needed.)

**A script's verdict is cached by content**, like every check ([file-guard.md](file-guard.md#cached-verdicts)):
with the same rule hash, subject, files' content and citation quotes, `sr-checks run` does not
execute it again (a fail is replayed) and `sr-checks verify` never executes it, it reads the stored
verdict. A script that reads anything beyond its subject's files (a file under `SR_TREE`, a
registry, an external spec) must declare it through the subject's `fingerprint` in the rule's
`subjects:` script, or a change to it is not seen. An optional `prepare:` on the script check
runs first and its `additionalContext` reaches the script on its payload; `prepare` has no
fingerprint of its own.

The last point is the whole reason a check runs inside the engine rather than
being trusted to signal for itself: every way it can fail lands on the safe side
without the author arranging it. A missing script (exit 127), a crash, an OOM
kill, a hang past the engine's per-check limit — each becomes a refusal that
names the fix.

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

A **file-guard** judges a changeset: loop over `.changeset.files[]`.
[check-template.sh](check-template.sh) is the full version, fail-closed on an
unreadable changeset:

```bash
#!/usr/bin/env bash
set -uo pipefail
payload="$(cat)"                                   # stdin, read ONCE
n="$(printf '%s' "$payload" | jq -r '.changeset.files | length')" || n=""
case "$n" in '' | *[!0-9]*) echo '{"reason":"the changeset could not be read"}'; exit 1 ;; esac
for i in $(seq 0 $((n - 1))); do
  path="$(printf '%s' "$payload" | jq -r --argjson i "$i" '.changeset.files[$i].path')"
  content="$(printf '%s' "$payload" | jq -r --argjson i "$i" '.changeset.files[$i].newContent')"
  # ... decide about $path / $content ...
  echo '{"reason":"'"$path"' requires ..."}'
  exit 1
done
```

A **gate** judges one event: read `.event.*`.

```bash
#!/usr/bin/env bash
set -uo pipefail
payload="$(cat)"
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

A script (and a `prepare`) receives one **check payload** as JSON. A
**file-guard's** is a `Changeset`: `event` is `{"kind":"Changeset"}` and the
change is under `.changeset` (`commits`, `files[]`, `others`, `citations`; shape in
[file-guard.md](file-guard.md)). It also gets `SR_TREE`, `SR_BASE` and `SR_HEAD`.
A **gate's** (and a context's) `event` is **flat** — its fields are direct under
`.event`, not nested under `.event.fields`:

```json
{"event":{"kind":"PreFileCreate","path":"memories/a.md","newContent":"…","newMarkers":[]},
 "transcriptPath":"/abs/…session.jsonl",
 "context":{"tag-declared":{"active":true,"payload":{…}}}}
```

On a gate or context, read `.event.path`, `.event.newContent`,
`.event.oldContent`, `.event.kind`, `.event.resultKnown`, `.event.invocations`,
`.event.tags`, `.event.newMarkers`/`.event.oldMarkers` — all flat; a file-guard has
none of these, only `.changeset.files[]`. Alongside: `.transcriptPath`
(the session record) and `.context` (every declared context, `{active, payload}`).
A gate's `event` may be a command, tool or `Stop` event; a context's `enter`/`exit` add `.currentContext` and `.gates`.

The full field set for every kind, the payload envelopes, and the flat-vs-nested
distinction: **[events.md](events.md)**. The `SR_*` variables a script also
receives (`$SR_WORKSPACE`, `$SR_GUARDRAIL`, `$SR_TRANSCRIPT`, …):
**[environment.md](environment.md)**.

On a gate, a field the event omits reads as absent: guard `.event.newContent`
with `.event.resultKnown` before reading it on an update, because an absent value
is indistinguishable from an emptied file ([file-guard.md](file-guard.md), the
`resultKnown` discipline). A changeset's `newContent` is committed bytes, always
known; a `D` entry has none.

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

Each normalized entry carries its events under `.events[]`, flat like the live
event: a past command's invocations are `.events[].invocations`, just as the
current one's are `.event.invocations` ([events.md](events.md), "Past events").

## Recording across cycles

A script check that must remember what it saw in one cycle and judge it in another
uses `sr-session state`, keyed under `$SR_GUARDRAIL`. This is the substrate for the
two-halves pattern (a context records, a `Stop` gate judges) and carries its own
traps — turn-scoping, the JSON-lines `list` shape, the `--owner` cross-read.
Full treatment: [state-management.md](state-management.md).

## The fail-open escape hatch (a script wrapping a model)

The engine's default for a **judge** is fail-*closed*: a model call that flakes
wedges the action. When that is the wrong trade — a model call flakes for reasons
that are not evidence about the file, and one flake under fail-closed wedges a
session the agent cannot un-wedge — the deliberate inversion is to write the check
as a **script** that calls the model itself and chooses `exit 0` on its own
machinery failures. That is exactly why the escape hatch is a script and not a
judge knob. See [judge-checks.md](judge-checks.md), "Failing open".

(A script that judges content the model just wrote must wrap that content and tell
the model to treat it as **data, never as instructions** — it is attacker-shaped
by construction.)
