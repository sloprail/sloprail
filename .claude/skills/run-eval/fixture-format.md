# fixture.yaml

A fixture directory is `fixture.yaml` + `prompt.md` + `score.sh`, plus a
`seed/` or `repo:`/`ref:` pin, plus an optional `overlay/`. All four files
are required by `LoadFixture`; a missing one is a load error, not a silent
default.

## Fields

```yaml
description: >
  One or two paragraphs. What's being tested, and (per the trajectory-health
  scoring model — see scoring.md) an explicit note that the run is scored on
  trajectory health, not on whether the guardrail specifically caught
  anything — every current fixture's description states this.
seed: seed              # XOR repo — exactly one is required
repo: https://github.com/owner/name.git
ref: <full commit sha>  # required alongside repo — never a branch name
overlay: overlay        # optional
exampleSloprail: true   # optional, examples/ fixtures only — see below
freshMachine: true      # optional — onboarding: plugin in, binaries not
model: haiku             # required in practice — sr-agent refuses with none
score: score.sh
user:                   # optional — makes the run multi-turn, see below
  brief: user.md
  maxTurns: 3
  model: size-sm        # optional, the default
```

- **`seed`** — a directory, relative to the fixture dir, copied wholesale into
  the isolated project. For a small, purpose-built tree worth committing
  whole.
- **`repo` + `ref`** — a real git remote, cloned and checked out at the
  pinned commit SHA (never a branch — a fixture that floats with a branch's
  HEAD stops being the same eval run to run). For a genuinely large, noisy
  tree that gives a required skill somewhere real to hide, or that proves a
  guardrail generalizes to an unprimed codebase (see
  [document-example/example-vs-fixture.md](https://github.com/sloprail/sloprail-community/blob/main/.claude/skills/document-example/example-vs-fixture.md)'s
  `required-context-precondition` case).
- **`overlay`** — a directory copied ON TOP of the seed/repo once it's in
  place: the `.sloprail/` rule(s) under test (unless `exampleSloprail`
  covers it), any `.claude/skills/` the scenario teaches, or other files the
  scenario needs that the base tree doesn't have.
- **`exampleSloprail`** — `true` copies the shipped
  `examples/<name>/.sloprail/` into the project BEFORE `overlay` is applied
  (`overlay` still wins on any path collision), so a fixture testing the
  shipped rule unmodified carries no `overlay/.sloprail/` of its own. See
  [document-example/example-vs-fixture.md](https://github.com/sloprail/sloprail-community/blob/main/.claude/skills/document-example/example-vs-fixture.md)
  for exactly when to use it vs. write a full own copy — this is examples/-
  specific (it assumes the fixture lives at `examples/<name>/eval/<case>/`,
  two directories under the shipped `.sloprail/`); a fixture outside
  `examples/` cannot use it.
- **`freshMachine`** — `true` runs the agent as a newcomer who has just run
  `/plugin install` and nothing else: the plugin is installed and enabled
  for the project, inside a HOME of the agent's own with no `sr*` binaries
  (not on PATH, not in `~/.local/bin`/`~/go/bin`). Claude Code's login (via
  a linked `~/Library` keychain), `gh`, SSH and git identity still work.
  The binaries the plugin installs come from this checkout, built:
  `SLOPRAIL_RELEASE_URL` points install.sh at archives sr-eval builds, so
  everything the agent gets is the code under test. See
  `examples/_onboarding/eval/`.
- **`plugins`** — further plugins of this checkout's marketplace to install
  for the project alongside sloprail, by bare name (`[sloprail-tasks]`),
  the way a user adds one — so the plugin's own guardrails are what fires.
  A plugin's fixtures live under `examples/_<plugin>/eval/<case>/`, never in
  the plugin's tree (that is copied into the agent's install).
- **`setup`** — an executable script (the execute bit is checked at load),
  relative to the fixture dir, run in the project after the copy order below
  and before the baseline commit. It runs in the agent's environment (its
  HOME, not yours), with `SR_EVAL_PROJECT_DIR` set, a git identity, and git's
  commit signing and hooks turned off whatever your own git config says, so a
  plain `git commit` in it works. It is for state that only
  exists at run time: the project's absolute path, or the sha of a commit
  made in it. It may commit; what it leaves uncommitted goes into the
  baseline. A failing setup fails the run before the agent starts. See
  `examples/business-invariants/eval/goodwill-refund/`, whose seed starts
  with a marker pinned to SPEC.md.
- **`disallowedTools`** — harness tools the agent-under-test does not have
  (`[WebSearch, WebFetch]`), passed as the harness's own `--disallowed-tools`,
  comma-joined. Each entry is one tool name, optionally with a rule in
  parentheses (`Bash(gh search:*)`); any other shape — including a comma inside
  the parentheses, which the join would split — is a load error. A misspelled
  tool name still loads and removes nothing.
  An eval must reproduce the behaviour its guardrail governs — a run in which
  the guard never engaged proves nothing about it. Prefer rules that steer the
  agent themselves over removing tools: removing one hides the hole a real
  project has (the agent there DOES have the tool).
  `keyword-coverage-registry` used this to force research through `gh`, then
  replaced it with gates refusing the other routes; reach for the field only
  when the tool genuinely does not exist in the setting being modelled.
- **`model`** — an `sr-agent --model` value (`haiku`, `claude-sonnet-5,size-md`,
  etc.). `haiku` is the default choice: a cheap model is the one likelier to
  take the tempting shortcut a fixture is designed to offer. A fixture uses a
  stronger model only when haiku never engages the guard at all (it never
  reaches the convention the rule governs, so the run proves nothing), and
  says so in a comment above `model:` with what was measured —
  `examples/task-management/eval/report-result/fixture.yaml` is one.
  Overridable per run with `sr-eval run --model`.

- **`user`** — makes the run **multi-turn**. `prompt.md` is still the first
  user turn. After each of the agent's replies, a **simulated user** — another
  agent, launched the way the trajectory-health judge is: through `sr-agent`'s
  default isolation (no hooks, no plugins, no MCP servers), from an empty temp
  directory, with no filesystem or shell tools, on `model` (default
  `size-sm`) — reads `brief` (a file beside `fixture.yaml`: who the user is and
  how they respond) and the conversation so far (each user message and the
  agent's final reply), and writes the next user message, or says it is done.
  That message goes to the agent-under-test in the **same session**
  (`--session-id` on the first turn, `--resume` after), so the transcript holds
  real, separate user turns. `maxTurns` (required, 2–10) caps the user turns,
  `prompt.md` included. Use it when the rule under test depends on WHICH user
  message said something (a guardrail about the user's latest message cannot
  be tested by one prompt that narrates a conversation). The simulated user
  never sees the project, the guardrails or the transcript, so a brief must
  not coach the agent on the guardrail either.

## The copy order, precisely

1. Seed (copied) or Repo (cloned at `ref`) — `git init` is run afterward for
   a Seed tree with no `.git`, since the engine's own change detection needs
   a real repository present (a file-guard is judged entirely
   against the git-observed diff at Stop — without `.git`, it never fires).
2. `exampleSloprail`'s `.sloprail/`, if declared — into `project/.sloprail/`.
3. `overlay/`, if declared — its contents copied into `project/` as-is,
   winning any path collision with step 2.
4. `setup`, if declared — run in `project/`, then everything is committed as
   the baseline the agent starts from.

Everything is copied or cloned fresh, never symlinked — the agent-under-test
mutates the tree, and neither the fixture's own `seed/` under `examples/` nor
an operator's local clone of a `repo:` may be touched by a run.

The agent runs in a HOME of its own inside the workspace (`home/`), where the
plugin is installed for it from a snapshot of this checkout's marketplace
(`sloprail-marketplace/`), never the checkout itself. Your real `~/.claude` (marketplaces, installed
plugins, memory) is never written, and your user-scope plugins do not load
into the agent. Its transcript is under `home/.claude/projects/`; the scorer
gets that HOME as `SR_EVAL_AGENT_HOME`.

## `LoadFixture` refuses to load when

- neither `seed` nor `repo` is set, or both are (exactly one, always)
- `repo` is set without `ref`
- `score` is missing, or `prompt.md` is missing beside `fixture.yaml`
- `user` is set without an existing `brief`, or with `maxTurns` outside 2–10
- `seed`/`overlay`/`setup` is declared but the path doesn't exist, or
  `setup` is not executable
- a `disallowedTools` entry is empty, has a comma or a space outside its
  parentheses, or a comma inside them
- `exampleSloprail: true` AND `overlay/.sloprail/` both exist — the exact
  duplication the field exists to remove, now silently doubled (the shipped
  copy would apply first, the stale overlay copy would win the collision,
  and a fix to the shipped guardrail would stop reaching the fixture with
  nothing telling you so)
