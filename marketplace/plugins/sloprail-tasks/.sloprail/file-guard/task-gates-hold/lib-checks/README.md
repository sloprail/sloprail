# Reusable deterministic checks for `.sh` gates

A `gates/<name>.sh` gate is an ordinary executable — task-gates-hold runs it
and treats exit 0 as PASS, no parsing, no special grammar. There is
deliberately no "named check" indirection layer the way an earlier design of
this feature considered (a `check: "name"` field resolved through a
dispatcher): a `.sh` gate file already IS the deterministic check, in the
plugin's own native format, and task-gate-is-grounded is what stops it being
a rubber stamp — see that guard's judge, which specifically rejects a
trivial/unconditional gate script. Adding a second layer of indirection on
top would only reintroduce the same trust question one level removed (is the
dispatcher's own resolution safe) for no new capability a plain script does
not already have.

What IS useful to share is the boring, repeatedly-needed PARTS of a gate
script — resolving a path under the project root, reading another task's
status, asking `gh` for a repo's visibility — so a gate author is not
re-deriving the same few lines of bash in every gates/*.sh file. These are
plain shell FUNCTIONS, sourced with `. "$(dirname "$0")/../../../task-gates-hold/lib-checks/checks.sh"`
(or copied inline — a gate is free to not use them at all), never invoked as
a `check:` name from frontmatter or any other indirect-by-name mechanism.
Using them does not exempt a gate from task-gate-is-grounded's judge: sourcing
`file_exists "memories/launch/video.mp4"` and exiting non-zero when it
returns false is exactly the kind of real, traceable, conditional check the
judge is looking for; wrapping the SAME trivial-pass pattern behind a
library call would not read any differently to it.

## What's here

`checks.sh` — sourceable shell functions, each returning 0 (holds) or 1 (does
not), with a reason on stdout when it fails:

- `file_exists <repo-relative-path>` — the file exists in the working tree.
- `task_is_done <group/task-name>` — the named task no longer exists as a
  task folder (mirrors task-dependencies-resolve's own "done means deleted"
  reading, for a gate that wants to wait on a task NOT listed in the
  dependent's own `depends_on`).
- `unit_has_status <topic>/<NN_name> <status>` — the content unit at
  `memories/topics/<topic>/units/<NN_name>/UNIT.md` has frontmatter
  `status: <status>` exactly.
- `gh_repo_visibility_is <owner/repo> <public|private>` — `gh repo view`
  reports the given visibility. Requires `gh` on PATH and its own auth; a
  missing `gh` or a failed call returns 1 (fail-closed — unreachable is not
  evidence the condition holds).

Each function takes `$SR_WORKSPACE` (or `PROJECT_ROOT`, settable for testing)
as the project root. See `checks.sh` for the exact contract.
