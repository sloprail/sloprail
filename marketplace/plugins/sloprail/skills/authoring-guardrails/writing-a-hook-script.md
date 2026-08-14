# Writing a hook script

The contract a script satisfies — stdin shape, exit statuses, how a reason is
found — is in [SKILL.md](SKILL.md). This is the shell-level detail of writing
one, which only matters once you are actually writing the script.

## The skeleton

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
fields. Run `sr-session query --help` for those. It reads the whole session, not
just the part not yet judged.

## A judge hook

A rule whose question needs a model reads its standard from `RUBRIC.md` beside
the declaration, found via `guardrailDir` on stdin. Keeping the standard in its
own file is what lets the prompt be assembled from the directory's contents
rather than edited: the frame stays put and the criteria are files beside it.

Judge hooks fail open on their **own** machinery — no model binary, a timeout,
an unparseable verdict — because a model call flakes for reasons that are not
evidence about the file, and under fail-closed one flake wedges a session the
agent cannot un-wedge by fixing anything. The verdict itself still fails closed.
Say which is which in the script, so the next reader can tell a deliberate
fail-open from a bug.
