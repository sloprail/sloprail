---
enforced: true
---

# Use the guardrail environment the engine sets, not the ambient one

**Mistake:** writing a ledger, a lock, or a state file to `$PWD` (or a
script-relative `$(dirname "$0")`), and resolving a project path from the ambient
directory instead of the workspace the engine names.

A check does not choose its own working directory reliably across the contexts it
runs in, and a guard that spawns `sr-agent` runs that agent's own hooks in a
different cwd again. A file written to `$PWD` lands somewhere the next invocation
does not look. The engine sets these so a script never has to guess
(`internal/dispatch/exec.go`):

| variable | what it is | use it for |
| --- | --- | --- |
| `$SR_GUARDRAIL_DIR` | the guard's own folder (absolute) | ledgers, locks, sibling files a check reads or writes — NOT `$PWD` |
| `$SR_WORKSPACE` | the project root | resolving a project-relative `.event.path` to an absolute path |
| `$SR_GUARDRAIL` | this guard's name | keying `sr-session state` to this guard's own scope |
| `$SR_SESSION_ID` | the conversation id | that same state keying, per session |
| `$SR_TRANSCRIPT` | the transcript path | a check that must read the conversation record (but prefer file events — see `prefer-file-events-over-trajectory`) |
| `SLOPRAIL_LAUNCHED_BY` | the guards already on this call stack | a check spawning `sr-agent` forwards it so the launched agent's hooks do not re-fire these guards and recurse |

**Instead:** write ledgers and state under `$SR_GUARDRAIL_DIR`; build an absolute
path as `"$SR_WORKSPACE/$path"`; key state with `$SR_GUARDRAIL` and
`$SR_SESSION_ID`; forward `SLOPRAIL_LAUNCHED_BY` across any `sr-agent` spawn.

**Flag** a ledger/lock/state file written to `$PWD` or `$(dirname "$0")` where
`$SR_GUARDRAIL_DIR` is meant, a project path resolved without `$SR_WORKSPACE`, or
a spawn of `sr-agent` that drops `SLOPRAIL_LAUNCHED_BY`.
