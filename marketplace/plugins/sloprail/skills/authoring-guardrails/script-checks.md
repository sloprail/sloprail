# Writing a script check

A script check is the deterministic way a rule decides: an executable that reads the payload
on stdin, and whose exit code is the verdict. Start from a template and change only `fine()`:
[check-template.sh](check-template.sh) for a file-guard, [gate-check-template.sh](gate-check-template.sh)
for a gate that prevents a write. Each already reads its payload correctly and refuses what it
could not read, which a script written from scratch rarely does the first time.

## The verdict is the exit code

- **Exit 0 permits.** Print nothing.
- **Any other exit refuses**, with whatever the script said as the reason.
- **A script that cannot run refuses.** Missing, not executable, crashed, killed, or past its
  time limit: a rule that could not check must not read as approval.

The script is run directly, never through a shell, so it must be executable and start with a
`#!` line. The command in the YAML is a path plus plain arguments (`./staged.sh check`), with
no quoting, pipes or `$`; put that logic in the script. A relative path resolves from the
rule's folder, which is also the script's working directory.

## Saying why

On a refusal the agent is shown, in order of preference:

1. `{"reason": "…"}` as JSON on stdout;
2. any other text on stdout;
3. text on stderr (`echo "…" >&2; exit 1` is an ordinary refusal);
4. failing all that, the check's name and exit status.

Write the reason to the agent whose action was refused, and say what to do instead. The rule's
name is appended for you.

A check that could not do its job, because a tool it needs failed rather than because the
content is wrong, adds `"error": true`: `{"reason": "sr-test could not run", "error": true}`.
It still refuses, but a file-guard does not store it as a verdict, so the next run tries again.

## Writing the script

- Use `set -uo pipefail`, not `set -e`. Under `set -e` an ordinary non-zero from a `grep` ends
  the script halfway, and that exit is read as a refusal the rule never meant.
- Read stdin once, into a variable (`payload="$(cat)"`), and take every field from that.
- Treat a payload you cannot read as a refusal, never as "nothing wrong".

What the payload holds: an event for a gate or a context ([events.md](events.md#what-a-check-reads-on-stdin)),
a changeset of commits for a file-guard ([file-guard.md](file-guard.md#what-a-check-receives)).

## The environment

Every script a rule runs (a check, a `prepare`, a context's `enter` and `exit`) also gets:

| variable | holds |
|---|---|
| `SR_GUARDRAIL` | the rule's name, under which `sr-session state` keeps its records |
| `SR_GUARDRAIL_DIR` | the rule's folder, as an absolute path |
| `SR_WORKSPACE` | the repository root; prepend it to `.event.path` to reach the file |
| `SR_SESSION_ID` | the session |
| `SR_TRANSCRIPT` | the session's transcript, the same as `.transcriptPath` |
| `SR_AGENT_ID` | the sub-agent the action came from; empty in the main session |
| `SR_TREE` | file-guards only: a read-only copy of the committed head |
| `SR_BASE`, `SR_HEAD` | file-guards only: the range's two commits |

A file-guard can run outside any session, in CI, where `SR_SESSION_ID` and `SR_TRANSCRIPT` are
unset; a check that needs the transcript must refuse there rather than pass. A variable with no
value is left unset.

## Asking what the agent did

Some checks ask about the session rather than the event: was a skill loaded before this write,
did a real `git clone` run? Read the transcript, passing its path explicitly:

```bash
tp="$(printf '%s' "$payload" | jq -r '.transcriptPath')"
sr-session trajectory normalize --path "$tp" --events PreCommandInvoke | jq '…'
```

`sr-session query --where '<expression>'` filters the transcript's raw entries; see its
`--help`. Past events are shaped like the live one ([events.md](events.md#past-events)).

A check that must remember something from one cycle to judge it in another keeps it in
`sr-session state` ([state-management.md](state-management.md)).
