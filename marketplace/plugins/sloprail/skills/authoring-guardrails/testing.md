# Testing a rule

A rule you wrote is proved by scenarios kept beside it, run by two commands that need nothing but
sloprail's own binaries, the project's `.sloprail/**`, bash and git. No Go test harness, no mock
agent, no model call.

```bash
sr-checks test [<rule>...]      # run the cases beside the rules, report what the engine did
sr-checks doctor [<rule>...]    # every rule loads, is proved by cases, and the cases pass
```

A change to a rule is "doctored" before any judge is asked about it: the shipped
`sloprail/file-guard/rule-tests` runs `sr-checks doctor` on each rule a commit touches
([The shipped gate](#the-shipped-gate-and-ci)).

## What a case is

```
.sloprail/<nature>/<name>/tests/<case>/
    case.yaml          what the case expects
    setup.sh           pure bash + git: builds the repository
    trajectory.yaml    optional: normalized events the session goes through
```

A case runs in a throwaway sandbox: a fresh git repository, home, engine state and session record
under one temp directory. It never touches the real project or the real session.

- **Without a trajectory** the case judges the commits `setup.sh` made, through the real
  `sr-checks run --base <base> --head HEAD` path. That is the test of a **file-guard**.
- **With a trajectory** each event goes through the same dispatch the hooks use, in order: contexts
  activate and persist across events, gates refuse, a Stop runs the real Stop path (commit required,
  the session's tracked ranges verified, the Stop gates, the context exits), sub-agents have their
  own hooks. That is the test of a **gate**, a **context**, and any composition of them.

The trajectory is written in the engine's own vocabulary, the flat events of
[events.md](events.md), never in any harness's payload format. So one case holds whichever harness
the project runs on (Claude Code, Codex, Cursor, ...): the adapters from a harness's payloads to
these events are proved once per harness in the engine's own tests, and a rule is proved once, here.

Judges never call a model. Each is answered from the case's canned verdict ([Judges](#judges)).

## case.yaml

```yaml
description: a TODO in a doc is refused          # optional, for the report
expect: refuse                                   # refuse | permit
reason_contains: still has a TODO                # a string or a list; the refusal must hold each
base: main                                       # optional: the range's start (default below)
with: [context/research-run]                     # other rules of this tree to load alongside
judges:                                          # canned verdicts, see Judges
  judge.md.j2: {pass: false, reasoning: too curt}
user_says: ["please add a docs page"]            # what the user said, for citations
contexts:                                        # after the trajectory: active | inactive
  research-run: active
```

Every key is checked: a mistyped key (`exepct:`) fails the case instead of asserting less than its
author believes. A case that expects nothing is refused.

| key | meaning |
|---|---|
| `expect` | Without a trajectory: the verdict of the file-guards over `base..HEAD`. With one: the verdict of its **last event step**, unless that step has its own `expect:` (then say it there; naming both is an error). |
| `reason_contains` | What the refusal must say: the rule's own words, not a model's. Skipped under `--live-judges`. |
| `base` | A file-guard case's range starts here. Default: the ref `base` if `setup.sh` made one (`git tag base`), else `HEAD~1`. |
| `with` | The rule under test is the only rule loaded, plus these (`<nature>/<name>`) and the contexts it `require`s. A gate that reads `context["x"]` lists `context/x`; a gate that sources a sibling rule's script lists that rule. Everything else in the project is out of the sandbox, so a case is about one rule. |
| `judges` | See [Judges](#judges). |
| `user_says` | The user's own messages in the session record, so a `Sloprail-Cites-User:` trailer or `sr-session trajectory cite '<quote>'` resolves ([grounding.md](grounding.md)). |
| `contexts` | The state each named context must be in once the trajectory has run. Needs a trajectory. |

## setup.sh

Pure bash and git, run with `bash` in the sandbox's repository as the working directory. The repository
already exists, with the rule under test (and its `with:` rules) committed as its first commit, tagged
`rules`: the project's rules stand before the case's own history begins. `setup.sh` builds the world.

```bash
set -e
mkdir docs
echo 'TODO write this' > docs/a.md
git add -A
git commit -q -m "add the doc"
```

- A git identity is set. There is no remote and no network.
- `$SR_TEST_CASE_DIR` is the case's folder (copy fixtures from it), `$SR_TEST_RULE_DIR` the rule's real
  folder, `$SR_TEST_PROJECT_ROOT` the project it belongs to.
- For a file-guard case, `setup.sh` makes the commits of the range. `git tag base` before the change
  marks where the range starts.
- For a trajectory case, `setup.sh` builds the world the **session starts in**; the changes are the
  trajectory's `run:` steps.
- Several cases share helpers by sourcing a file beside them, `tests/_lib.sh` (a plain file in `tests/` is
  not a case). `setup.sh` runs as `bash <its path>`, so `$0` finds it, and a helper that loads only
  partly must not be trusted, so it ends with a loaded sentinel the caller checks:

  ```bash
  unset my_lib_loaded
  . "$(dirname "$0")/../_lib.sh" || exit 2
  [ "${my_lib_loaded:-}" = 1 ] || exit 2     # _lib.sh's last line is my_lib_loaded=1
  ```
- Build a fixture *rule* with heredocs in `setup.sh`, not as files under `tests/`: a fixture file named
  `gate.yaml` there is read as a misplaced declaration by `sloprail/gate/misplaced-declaration`.

## A file-guard case

A file-guard judges a range, so its case is a range. The rule, a script and a judge:

```yaml
# .sloprail/file-guard/memo-title/file-guard.yaml
match: memos/**
checks:
  - script: ./check.sh        # refuses a memo with no "# Title" line
  - judge: ./judge.md.j2      # is the memo polite?
```

```yaml
# .sloprail/file-guard/memo-title/tests/refuse-untitled/case.yaml
expect: refuse
reason_contains: must start with a '# Title' line
```

```bash
# .../tests/refuse-untitled/setup.sh
set -e
mkdir memos
echo "no title here" > memos/a.md
git add -A
git commit -q -m "add memo"
```

The cheap script refuses before the judge is asked, so this case stubs no judge (and a case that
stubbed it would fail, see [Judges](#judges)). The case that reaches the judge stubs it:

```yaml
# .../tests/refuse-impolite/case.yaml
expect: refuse
reason_contains: rude
judges:
  judge.md.j2:
    pass: false
    reasoning: the memo is rude
```

```yaml
# .../tests/permit-polite/case.yaml
expect: permit
judges:
  judge.md.j2:
    pass: true
    prompt_contains: ["Dear all, thank you."]    # what the judge would have been shown
```

## trajectory.yaml

An ordered list of steps. Each is one of:

```yaml
- kind: PreFileCreate               # an event: its kind and its flat fields (events.md)
  path: memories/a.md
  newContent: "..."
  expect: refuse                    # optional: the verdict this event must get
  reason_contains: "..."            # optional
  contexts: {research-run: active}  # optional: context states right after this step
  agent: scout                      # optional: the event comes from this sub-agent
- run: |                            # bash in the repository, between events
    mkdir notes
    echo findings > notes/research.md
    git add -A && git commit -q -m notes
- subagent: start                   # or stop; stop can carry expect:
  id: scout
- checks_run: {}                    # the agent's `sr-checks run`: judge the file-guards over the session's commits
  expect: refuse
- contexts: {research-run: inactive}   # only an assertion
```

### Events

Write the facts only you know; the engine derives the rest exactly as the module for that kind does.

| kind | you write | derived |
|---|---|---|
| `PreFileCreate` | `path`, `newContent` | `newMarkers` (the `sr:` markers of the text), `resultKnown: true`, `citations: []` |
| `PreFileUpdate` | `path`, `newContent` | the above, plus `oldContent` and `oldMarkers` read off the file in the repository |
| `PreFileDelete` | `path` | `oldContent`, `oldContentKnown: true`, `oldMarkers` |
| `PreFileWrite` | as a create or an update | an alias: an update when the file exists, a create when it does not |
| `PreCommandInvoke` | `command` | `raw`, and `invocations` parsed by the engine's own command parser (pipelines, `&&`, subshells, `sudo`, `xargs`) |
| `PreToolUse` | `tool`, `input` | |
| `PostFileCreate` / `PostFileUpdate` / `PostFileDelete` | `path` | the settled file read off the repository (and the old bytes at HEAD) |
| `PostTagWrite` | `tags: [research]` (or `{label, seen}`) | |
| `Stop` | | |

A field you write wins: `resultKnown: false` (with no `newContent`) is the write the engine could not
compute, `newMarkers: [...]` overrides the derived markers. A field the kind does not carry is an error,
as is a Pre write with neither `newContent` nor `resultKnown: false`.

**When an event is dispatched.** A Pre event is dispatched at once, as a harness asks before every tool
call. A **Post** event is what a cycle left behind, so it is held and delivered with the next `Stop` of
the agent it came from (a refused Stop keeps them, the retry delivers them again, as the engine does).
`expect:` belongs on the Stop, never on a Post event.

Every Pre event is also written into the session's record before its hook runs, as a harness writes
a tool call. So a rule that asks the trajectory what the agent did sees it: a `PreToolUse` of
`Read` on a skill's page satisfies `require: [{skill, files}]`, a `Skill` tool use satisfies
`require: [{skill}]`, a `PreCommandInvoke` shows in `sr-session trajectory normalize`. File paths of a
tool call are made absolute, as a harness reports them.

### The session

The case starts the session after `setup.sh`: the `SessionStart` hook records the baseline, and the
agent's first tool call registers the repository, so **the commits a `run:` step makes are the session's
commits**. `checks_run` is the agent's `sr-checks run` (its range starts at the `base` tag, else the
`rules` tag; `checks_run: {base: <rev>}` overrides); the judges it asks are the case's. A `Stop` then
**verifies** the stored results over the session's tracked ranges, exactly as in a real session:

```yaml
- run: |                                # the session commits a TODO
    mkdir docs && echo TODO > docs/a.md
    git add -A && git commit -q -m doc
- checks_run: {}                        # the agent judges it ...
  expect: refuse
  reason_contains: still has a TODO
- kind: Stop                            # ... and the Stop reports the stored fail
  expect: refuse
- run: |
    echo done > docs/a.md && git commit -q -am "finish"
- checks_run: {}
  expect: permit
- kind: Stop
  expect: permit
```

Uncommitted work on a path a file-guard selects is refused at Stop as "commit required", and the Stop
gates, the context exits and the sub-agent registry all run in the engine's own order
([context.md](context.md#the-stop-order)).

### Sub-agents

`agent: <id>` on an event makes it that sub-agent's call; `subagent: start` / `stop` are its own hooks.
A sub-agent is its own session in the engine: its own record, its own contexts and state. Its `stop`
is its own Stop, judged by the Stop gates against its own state, and a refusal is the sub-agent's. Its
Post events are delivered at its stop.

## One example per nature

### Gate on an event

```yaml
# .sloprail/gate/no-curl/gate.yaml
on:
  - event: PreCommandInvoke
    match: any(event.invocations, .bin == "curl")
checks:
  - script: ./refuse.sh      # {"reason": "use the fetch tool, not curl"}, exit 1
```

```yaml
# .../no-curl/tests/refuses-and-permits/case.yaml
description: a curl anywhere in the line is refused, ordinary commands are not
```

```yaml
# .../no-curl/tests/refuses-and-permits/trajectory.yaml
- kind: PreCommandInvoke
  command: echo go && (cd /tmp && sudo curl -s https://example.com | head)
  expect: refuse
  reason_contains: fetch tool
- kind: PreCommandInvoke
  command: ls -la
  expect: permit
```

A pre-write gate reads the bytes, the derived markers and `resultKnown`:

```yaml
- kind: PreFileWrite
  path: config.env
  newContent: "API_KEY=abc\n"
  expect: refuse
- kind: PreFileWrite
  path: README.md
  resultKnown: false          # a sed -i the engine could not compute
  expect: refuse              # a gate that prevents must refuse what it cannot check
```

### Context

A context is proved by the states it ends a trajectory in. `research-run` opens on a `#research` tag and
closes when its note exists:

```yaml
# .../research-run/tests/a-tag-opens-it/case.yaml
contexts:
  research-run: active
# trajectory.yaml
- kind: PostTagWrite
  tags: [research]
- kind: Stop
```

```yaml
# .../research-run/tests/the-note-closes-it/case.yaml
contexts:
  research-run: inactive
# trajectory.yaml
- kind: PostTagWrite
  tags: [research]
- run: |
    mkdir notes && echo findings > notes/research.md
- kind: Stop
```

`sr-checks doctor` wants a case that leaves the context active and one that leaves it inactive.

### Sub-agent

A Stop gate refuses a turn that researched without writing its note. For a sub-agent, its own Stop is
judged by its own session:

```yaml
- subagent: start
  id: scout
- kind: PostTagWrite
  agent: scout
  tags: [research]
- subagent: stop
  id: scout
  expect: refuse
  reason_contains: notes/research.md
```

### Composed: a context, a gate and a sub-agent

The gate `research-needs-note` is bound to `Stop`, matches `context["research-run"].active` and
`require`s that context; `research-run` is the context above. One trajectory exercises all three:

```yaml
# .sloprail/gate/research-needs-note/tests/a-scout-writes-the-note/case.yaml
description: the root's refused Stop is satisfied by a sub-agent's note
with: [context/research-run]
```

```yaml
# .../trajectory.yaml
- kind: PostTagWrite             # the root declares research
  tags: [research]
- kind: Stop                     # no note yet: the gate refuses, the context stays open
  expect: refuse
  reason_contains: notes/research.md
  contexts: {research-run: active}
- subagent: start
  id: scout
- run: |                         # the scout writes and commits the note
    mkdir notes
    echo findings > notes/research.md
- subagent: stop                 # its own Stop is its own session: no research context there
  id: scout
  expect: permit
- kind: Stop                     # the retried Stop: the note is there, the gate permits
  expect: permit
  contexts: {research-run: inactive}   # and the context's exit closed it
```

## Judges

`judges:` maps a judge file of the rule (`judge.md.j2`; another rule's is `<nature>/<name>/<file>`) to its
canned verdict:

```yaml
judges:
  judge.md.j2:
    pass: false                      # the verdict
    reasoning: the memo is rude      # the sentence a refusal carries (required when pass is false)
    prompt_contains: ["Dear all"]    # optional: what the rendered prompt must hold
    optional: true                   # optional: the case does not require the judge to be asked
```

The judge's template is rendered for real, so a template that does not render fails the case as it would
live, and `prompt_contains` proves the change and the `prepare`'s context reach the question. Then the
stub answers. Three failures keep the stubs honest:

- a judge the engine asks that the case does not stub **fails the case** (a case never calls a model);
- a stub that is never asked fails it (the rule stopped reaching the judge), unless `optional: true`;
- a prompt that lacks a required substring fails it.

`sr-checks doctor` requires each judge a rule declares to be stubbed to pass in one case and to fail in
another.

`--live-judges` asks the real judges instead, for judge-accuracy runs: the case's `expect` is then checked
against the live verdict (`reason_contains` is skipped, the wording is the model's).

## Grounding

`user_says: [...]` puts the user's messages in the session record, so a citation resolves exactly as in a
session: a commit trailer for a file-guard that `require`s a citation, a chained
`sr-session trajectory cite '<quote>' && git push` for a gate:

```yaml
user_says: ["please push the branch"]
```

```yaml
- kind: PreCommandInvoke
  command: git push origin main
  expect: refuse                      # no citation
- kind: PreCommandInvoke
  command: sr-session trajectory cite 'please push the branch' && git push origin main
  expect: permit
```

## doctor, and what to see when a case fails

```
$ sr-checks doctor gate/no-curl
FAIL gate/no-curl  1 case(s)
       - missing: no case expects the rule to permit: a rule nobody has seen permit may refuse everything
       PASS refuses-curl                refuse: use the fetch tool, not curl (gate "no-curl")
```

`doctor` reports, per rule: a declaration that does not load (the loader's own faults), a case that cannot
be read, a rule with no case at all, a missing refuse or permit case, a judge stubbed only one way; then it
runs the cases. `--no-run` stops before running. `--allow-untested` lets a rule with no cases at all pass
as a warning (the rollout switch below). Exit 1 on any fault.

A failing case prints what it expected, what the engine did, and under it `engine said:`, the engine's own
stderr (a rule that failed to load, a check's diagnostics). `--keep` leaves each sandbox in place and prints
where, to look at the repository the case built. `--json` prints the results for a tool.

## The shipped gate, and CI

The plugin ships `sloprail/file-guard/rule-tests`: when a commit touches `.sloprail/<nature>/<name>/**` (the
rule, a script, a template, a case), it runs `sr-checks doctor` on that rule over the committed tree. It is
a script check, so it finishes before any judge of the same run starts: a rule change whose tests fail never
reaches `grounded-rule-changes`' model call. The verdict is cached per rule, fingerprinted by the rule's
whole folder as committed.

The rollout is strict where it costs nothing and gentle where it would only block:

| the rule at the range's base | required |
|---|---|
| did not exist | tests: a refuse case, a permit case, judges stubbed both ways, all passing |
| existed with tests | the same: the cases cannot be deleted or left to rot to get a change through |
| existed with no tests | grandfathered until its first case; once it has any, they must pass and cover |
| removed | nothing to prove (`grounded-rule-changes` judges removing a rule) |

`disabled: [sloprail/file-guard/rule-tests]` in `.sloprail/config.yaml` turns it off. In CI, run
`sr-checks doctor` (add `--allow-untested` while rules still lack cases) beside `sr-checks verify`.
The rules a *plugin* ships are tested in the plugin's repository:
`sr-checks doctor --plugin <name> --rules-dir <plugin>/.sloprail`.

## What a case does not prove

- It proves the rule against the engine's events. That the harness's adapter produces those events is the
  engine's own test, once per harness; a case written here holds on all of them.
- A Post event is what the case says it is, not derived from the tree: write the files with a `run:` step
  and name the event.
- The session record holds the user's messages and the tool calls of the Pre events. A tool's *output*
  (`Sloprail-Cites-Tool`) is not modelled yet.
- Cases run one at a time, each in processes of its own (a case takes a few seconds).
