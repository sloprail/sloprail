# The check environment

Every executable a rule runs — a **check script**, a **`prepare`**, a context's
**`enter`** and **`exit`** — is run by the engine with a set of `SR_*` variables
on its environment. They are cross-cutting: every guardrail script sees them,
whatever its nature, and they carry the facts a script needs that are not on the
payload's stdin (which guardrail it is, where the workspace is, which session).
This is the single reference for them; the other docs point here.

Ground truth is the engine: `internal/dispatch/exec.go` (`scriptCall.env()` sets
the `SR_*` variables) and `services/sr-session/provenance.go` (`LaunchedByEnv` and
its append/dedup). This doc mirrors it.

## The variables

| var | what it holds | when it is set |
|---|---|---|
| `SR_GUARDRAIL` | the rule's name — the keyspace `sr-session state` reads and writes under | on every check, prepare, enter, exit |
| `SR_GUARDRAIL_DIR` | the rule's own folder, absolute — where `RUBRIC.md`, `rules/`, sibling scripts resolve | on every check, prepare, enter, exit |
| `SR_WORKSPACE` | the repository root — prepend it to a `.event.path` to reach the file on disk | on every check, prepare, enter, exit |
| `SR_SESSION_ID` | the session id, so one session's state is not another's | on every check, prepare, enter, exit |
| `SR_TRANSCRIPT` | the session record (same value as the payload's `transcriptPath`) | on every check, prepare, enter, exit |
| `SR_TREE` | a **read-only snapshot of the range's `head`**, for a file-guard check to read files `match` did not select (a sibling spec, a test file) as committed, never the working tree | file-guard checks only |
| `SR_BASE`, `SR_HEAD` | the range's two SHAs ([file-guard.md](file-guard.md)) | file-guard checks only |
| `SR_SESSION_START` | the HEAD recorded when the session began (empty when none is), so a check can tell what stood before the agent worked | file-guard checks only |
| `SLOPRAIL_LAUNCHED_BY` | the guardrails whose checks are on the current call stack — the re-entry provenance | only when a check runs underneath a judging rule (see below) |

Each `SR_*` variable is **left unset when its value is empty**, deliberately —
"unset is diagnosable" is a stance a rule can test for. Outside a hook there is no
guardrail in scope, so `sr-session state` says so rather than guessing.

The engine appends these **after** the parent's environment, so the engine's own
answer wins over any stale value an outer process happened to export.

## `SR_GUARDRAIL` — the state keyspace

`sr-session state get|set|list` keys under `SR_GUARDRAIL` implicitly. Neither the
guardrail nor the session is an argument — both come from the environment — so a
script calls `state` with nothing but a key and cannot read a rule it was never
told about nor reach into another session:

```sh
prev=$(sr-session state get seen 2>/dev/null || echo 0)
sr-session state set seen "$((prev + 1))"
```

The one read that crosses the per-guardrail boundary is `state list --owner
<other>`, which a gate uses to read a paired context's registry — read-only, `list`
only. Full treatment in [state-management.md](state-management.md).

## `SR_GUARDRAIL_DIR` — the rule's own folder

The absolute path of the folder the rule's YAML sits in. A script already runs
**with that folder as its working directory** (a `./verify.sh` resolves there), so
`SR_GUARDRAIL_DIR` is for when a script needs the folder by absolute path — to read
a sibling `RUBRIC.md` or a `rules/` file from a context where the cwd may have
changed.

(The old format handed a script the guard's directory as a `guardrailDir` field on
stdin. It is now this environment variable — read it from the env, not the
payload.)

## `SR_WORKSPACE` — the repository root

Prepend it to a repository-relative `.event.path` to reach the settled file on
disk, or to resolve a project-relative path a check verifies against (a goal's
`verify.sh` under `<workspace>/goal/…`):

```bash
abs="${SR_WORKSPACE:-.}/$path"
[ -f "$abs" ] && body="$(cat "$abs")"
```

This is how a file-guard's after-check reads the **settled** bytes at `Stop`
rather than trusting what an event announced — see [file-guard.md](file-guard.md),
"Which bytes, at which moment".

## `SR_TRANSCRIPT` — the session record

The path to the session's transcript, the same value the payload carries as
`.transcriptPath`. Either source works; pass it **explicitly** to a trajectory
read — a read given no path fails closed rather than guess which session it is in:

```bash
tp="$(printf '%s' "$payload" | jq -r '.transcriptPath')"   # or "$SR_TRANSCRIPT"
sr-session trajectory normalize --path "$tp" --events PreCommandInvoke | jq '…'
```

## `SLOPRAIL_LAUNCHED_BY` — the re-entry provenance

This one is not for a rule to read; it is how the engine keeps a **judging** rule
from re-firing on itself, and you should know it exists so its behaviour is not a
surprise.

A judge runs `sr-agent`, whose own first `Write` fires `PreToolUse`, which runs
the same guard's dispatch — so without a guard the judging rule recurses on its own
launched agent (measured to depth 8 before an unrelated counter stopped it). The
engine sets `SLOPRAIL_LAUNCHED_BY` on every hook, **appending** the guardrail's own
name to whatever it already said, and the value is inherited across the exec into
the launched agent's own hooks. A hook firing inside that agent reads which rules
it is running underneath and the engine declines to enforce **those** — and only
those; every other rule still holds, because a launched agent editing the project
is still an agent nobody else is watching.

- It is a **colon-separated list**, not a single name: a launched agent may itself
  launch one, so nesting appends and the innermost level knows every rule above it.
  (A guardrail name is a directory name, so it cannot contain `:`.)
- A name already present is not appended twice — a value that grew unbounded across
  a long chain would eventually be an environment too large to exec.
- It is left **unset** when empty, which is correct for a check that cannot launch
  an agent.

You do not set or read this yourself. It matters when you are debugging why a
judging rule did (or did not) re-fire underneath its own agent — the reason lives
here. See [judge-checks.md](judge-checks.md) for the judge substrate.
