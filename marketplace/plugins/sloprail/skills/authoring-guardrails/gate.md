# Gate

A gate is a **checkpoint on an event**. It wakes on the events its `on:` names,
optionally requires some precondition, runs its checks, and **blocks** if they
refuse. Unlike a file-guard it is **one-shot** — it fires on the event, decides,
and is done; it does not re-fire until a file settles.

```
.sloprail/gate/<name>/gate.yaml
```

```yaml
# require-skill-topics — writing under memories/topics/ is blocked until
# document-topic was loaded this session. `require` is the whole rule; no checks.
on:
  - event: PreFileWrite
    match: event.path startsWith "memories/topics/"
require:
  - skill: document-topic
```

```yaml
# verify-artifact-produced — on Stop, if a tag was declared this turn, the
# matching artifact must have landed. Reads a sibling context's registry.
on:
  - event: Stop
    match: context["tag-declared"].active
require:
  - context: tag-declared
checks:
  - script: ./verify-tag-and-artifact.sh
```

Three keys. `on` is the list of triggers — each an `event` kind and an optional
`match`. `require` is a list of preconditions that must already hold. `checks` is
the list of checks — each a script ([script-checks.md](script-checks.md)) or a
judge ([judge-checks.md](judge-checks.md)). A gate needs `on`; the other two are
optional, but a gate with neither `require` nor `checks` decides nothing.

## The gate scope: `event.*`, nested

A gate's `match` — and its checks' stdin — **nest the event under `event`**:
`event.path`, `event.invocations`, `event.tags`, plus the `context` map. This is
the asymmetry with a file-guard, whose match reads `path` bare. A gate is not "a
file at a path" by default, so there is **no glob shorthand** — a gate narrowing
on a path writes it out: `event.path startsWith "memories/decisions/"`.

`match` is checked against the fields of the kind the trigger fires on, so
`event.invocations` type-checks on a `PreCommandInvoke` trigger and
`event.path` on a `PreFileWrite` one. A name the kind does not declare
(`event.paht`) is refused at load.

## What a gate triggers on

Any **pre-action** event kind, plus **`Stop`** — never a `Post` variant (a gate
that already happened is too late to gate). [events.md](events.md) has the full
per-nature admission table and every kind's fields; the ones a gate is written on:

### An event about to happen — the pre-action gate

The gate refuses **before** the action, preventing it. Examples:

- **`PreFileWrite`** — the alias the engine expands to `PreFileCreate` +
  `PreFileUpdate`, so one trigger covers both. `event.path startsWith
  "memories/decisions/"`. (A gate cannot use `PostFileWrite` — that alias is
  context-only.)
- **`PreFileDelete`** — a file about to be deleted (`rm`, `git rm`, `mv` away, a
  recursive removal expanded per file). Not part of the `PreFileWrite` alias.
- **`PreCommandInvoke`** — a shell command line about to run. It carries the
  **flattened `invocations`** it parsed (below).
- **`PreToolUse`** — a tool call about to run.

### Preventing a write or a delete — `PreFileWrite` and `PreFileDelete`

This is where writes and deletes are **prevented**. A file-guard judges only what
settled at Stop; the gate is the one that refuses before the bytes land or the
file goes. (A `preventive:` key on a file-guard no longer exists — such a
declaration is refused at load. Split it into a gate like the ones below plus a
plain file-guard for the settled result; see [file-guard.md](file-guard.md).)

```yaml
on:
  - event: PreFileWrite            # PreFileCreate + PreFileUpdate
    match: 'event.path startsWith "spec/"'
  - event: PreFileDelete           # a delete is not a write: name it to cover it
    match: 'event.path startsWith "spec/"'
require:
  - citation: {source_types: [user]}
checks:
  - script: ./verify.sh
```

- **What it sees.** A create carries `event.newContent`, `event.resultKnown`,
  `event.newMarkers`, `event.citations`; an update adds `event.oldContent` and
  `event.oldMarkers`; a delete carries `event.oldContent`,
  `event.oldContentKnown`, `event.oldMarkers`, `event.citations` — the bytes about to
  be lost ([events.md](events.md)).
- **Every file, once each.** One tool call can change several files (`rm a.go
  b.go`, two `sr-file` calls joined by `&&`) and runs whole or not at all. The gate
  is run once per matching file event, so a call whose *second* file fails is
  refused before it runs and none of them is changed; the one refusal names every
  refused file. A command, a tool call or `Stop` still wakes a gate once.
- **An unknown result is yours to refuse.** The engine adds nothing for you here.
  When it cannot compute what a write will leave (`sed -i`, `git apply`, a
  notebook create, an `sr-file` line it could not resolve), the event carries
  `event.resultKnown: false` and an empty `event.newContent` — the same observation as a write
  that empties the file. A gate whose decision reads the content says what to do in
  its `match` or in a check, or the write lands unchecked. To refuse them outright,
  select them in the trigger and let a check refuse:

  ```yaml
  on:
    - event: PreFileWrite
      match: 'event.path startsWith "spec/" and not event.resultKnown'
  checks:
    - script: ./cannot-verify.sh   # prints {"reason": "…write the file directly"}, exit 1
  ```

  A gate that reads `event.newContent` in a check refuses on `event.resultKnown != true` first
  (see [file-guard.md](file-guard.md), "The resultKnown discipline"). A
  `PreFileWrite` or `PreFileDelete` gate holds only cheap checks — a `require`, a
  script; the judge belongs to the file-guard of the same name, which judges the
  settled file at Stop. The exception is a gate whose judge is not about a file
  write: a `Stop` gate or a `PreCommandInvoke` gate may keep its judge
  (`examples/action-proof` `screenshot-proves-fields`, `examples/no-unasked-commit`
  `require-live-ask-for-commit`).
- **Grounding.** `require: [{citation: …}]` works on a `PreFileWrite` or
  `PreFileDelete` gate exactly as on any gate ([grounding.md](grounding.md)); when
  an `sr-file` line could not be resolved, the refusal quotes what `sr-file` said.

### The turn as a whole — the Stop gate

`Stop` fires once when a work cycle ends, **last and unconditionally** — whether
or not anything changed. This is the right trigger for a rule about the *result*
of a turn ("the turn promised an artifact; did it produce one?").

A Stop refusal is reported to the agent as a blocking error on the cycle, and the
cycle's read mark does not advance — so the next Stop judges the same span again,
and a rule that stays unsatisfied stays reported rather than scrolling away.

The agent's corrected reply fires `Stop` again, and that retry is **judged like
any other Stop**: replying twice does not get the agent past a rule. A reply that
still breaks the rule is refused again, so the loop runs until a reply passes.
The project caps how many refusals in a row it will take, in `.sloprail/config.yaml`:

```yaml
stop_hook_block_cap: 8   # the default, matching Claude Code's own cap
# 1 — refuse once, then let the retry end un-judged
# 0 — no engine cap (Claude Code's CLAUDE_CODE_STOP_HOOK_BLOCK_CAP still applies)
```

When the cap is reached the turn ends with the refusal still standing, and the
engine says so on stderr. Nothing is marked judged, so the next cycle sees the same
work again.

`Stop` carries **no fields** — the end of a cycle is about the cycle, not one
file. So a Stop gate has nothing on the event to narrow on; it establishes its
subject another way:

- By reading a **context**'s accumulated state — the usual pattern. A context
  logs what it saw into `sr-session state`; the Stop gate reads that registry
  back (below, and [state-management.md](state-management.md)). This is why a
  Stop gate's `match` so often reads `context["…"].active`.
- Or by asking `sr-session query` / `sr-session trajectory` about the transcript
  (via `.transcriptPath`).

Because a Stop gate fires every cycle, it can wedge a session wholesale rather
than for one file. Refuse on the rule's own logic, and permit when the rule's
plumbing fails ([state-management.md](state-management.md), "fail closed on the
logic, open on the plumbing").

## Inside `PreCommandInvoke`: the flattened invocations

One command line is rarely one program. A pipeline, an `&&` chain, a subshell, a
`sudo` or an `xargs` each nest invocations inside a single string. The module
walks that structure once and emits **every invocation it finds, flattened**, so
no rule has to recurse through shell syntax — and nesting an invocation one level
deeper does not defeat a rule written against it.

So a gate narrows on the `invocations` list, not the raw line:

```
any(event.invocations, .bin == "curl")
not any(event.invocations, .bin == "npm")
len(event.invocations) > 1
any(event.invocations, .bin == "rm" and any(.argv, # == "-rf"))
```

Each invocation's fields — `.bin`, `.argv` (both with declared element shapes, so
a mistyped key inside a predicate is refused at load) and the **open** `.flags`
map (verified against nothing, so a mistyped flag evaluates false forever — cause
the command and watch it fire before trusting one) — are set out in full in
[events.md](events.md). The gate-specific point is only that you match against the
flattened list.

### Example: a shipped command gate

The plugin's `sloprail/gate/no-merge-over-refusals` is a command gate with all its
policy in YAML and one script. It matches `gh pr merge` (any flags, `--admin` too),
and its `require: citation` carries a `when` script that applies the requirement
only when the branch being merged has open refusals in this session's check results,
judged at that branch's tip (it reads them with `sr-checks sql`). A merge with none is
untouched; one with open refusals is refused until the fix is committed and judged,
or until the user's own words are cited (`sr-session trajectory cite '<their words>' &&
gh pr merge ...`). It is on by default and switched off like any shipped rule, under
`disabled:` in `.sloprail/config.yaml`.

### The resolution floor

A program named by a variable, a payload decoded and piped to a shell, splitting
that depends on the runtime `IFS`: none of these can be known without running
them, and running them is exactly what a guardrail must not do. What can be seen
is emitted; what cannot is left alone rather than guessed at. This is a
correctness aid, **never a security boundary** — a rule that assumes every way of
invoking a program is visible here believes more than the parser promises.

There is no `Post` counterpart to `PreCommandInvoke`. A file has a settled state
a diff establishes afterwards; a command that already ran has no equivalent —
what it changed shows up as the file events, which is where a rule about
consequences belongs.

## `require`: a precondition that must already hold

`require` is a list of things that must be true **before** the gate's checks even
run. If a requirement is unmet, the gate refuses on that alone — `require` can be
the whole rule, with no `checks` at all.

```yaml
require:
  - skill: document-topic        # this skill was loaded this session
  - skill: authoring-guardrails  # AND this specific page inside it was read
    files: [file-guard.md]
  - context: tag-declared        # this context is currently active
  - citation: {source_types: [user]}  # the action cites the user's words
```

Three forms in use:

- **`skill: <name>`** — the named skill was loaded this session. "Writing under
  `memories/decisions/` is blocked until `document-strategy` was loaded" is a
  gate whose `require` is exactly that, and nothing else. A separate gate per
  prefix→skill pairing, rather than one gate branching internally, because
  `require` binds to the **whole gate**.
  - **`files: [<name>, …]`**, alongside `skill`, optional — page(s) INSIDE
    that skill (relative to its own directory) that must ALSO have been read
    (a Read tool_use, or a file-reading Bash command). Loading a skill only
    guarantees its `SKILL.md` was read, not any page it merely links to.
- **`citation: {source_types: [...]}`** — the action carries a citation that
  resolved in one of the named pools: `user` (the user's own words) or
  `tool_result` (a tool's output). The pools are always named. For a command, the agent chains a cite in front of it:
  `sr-session trajectory cite '<exact quote>' && git push`. Only a gate on
  `PreCommandInvoke` or a `PreFile*` kind may require one; on `Stop` or
  `PreToolUse` it is a load error. See [grounding.md](grounding.md).
- **`context: <name>`** — the named context is active. This is also what makes a
  Stop gate's cross-context read safe: `require: [{context: tag-declared}]`
  guarantees that context **entered this cycle before** this gate's check runs,
  so the registry the check reads back is current. `match` reads
  `context["…"].active`, but that read is only *meaningful* once the context has
  actually run its enter this cycle — which `require`'s ordering guarantees.

A gate that reads a context's `sr-session state` registry via `--owner` **without**
a matching `require` reads stale, prior-cycle state — a footgun. The `--owner`
read supplies the entries; `require` supplies the ordering. See
[state-management.md](state-management.md).

Note the two roles the context plays in the artifact example above: `match:
context["tag-declared"].active` skips the gate declaratively when the context
never activated, and `require: [{context: tag-declared}]` orders the context's
enter before the check. A sibling gate can catch the opposite case — *no* tag at
all — with `match: not context["tag-declared"].active` and no `require` (there is
nothing for the context to have run first).

## Reading a context's registry from a gate's check

A Stop gate's check reads back what a paired context accumulated. The context
logs each subject under its own name into `sr-session state`; the gate reads the
group with the cross-guardrail `--owner` read (`list` only, read-only):

```bash
# state list emits JSON-LINES, so SLURP with `jq -s` before treating it as one.
entries="$(sr-session state list --owner tag-declared 2>/dev/null)"
tags="$(printf '%s' "$entries" | jq -s -r '[.[] | select(.key | startswith("tag:"))] | .[].key | ltrimstr("tag:")')"
```

The gate's own `require: [{context: tag-declared}]` is what makes those entries
current. Full treatment — the JSON-lines shape, `--owner`, and why `require` is
load-bearing — in [state-management.md](state-management.md).

## Turning one off

Keep the folder; disable the gate from `.sloprail/config.yaml` by its qualified
name (the same mechanism that disables a plugin's gate):

```yaml
disabled:
  - <plugin-or-project>/gate/<name>
```

A gate can also **ship off**: `enabled: false` in its `gate.yaml` makes it inert until the
project switches it on, by qualified name, in the same config:

```yaml
enabled:
  - sloprail/gate/judge-before-push
```

(The plugin's `judge-before-push` gate is the example: it runs `sr-session judge` before a
`git push` / `gh pr create` and refuses while a file-guard refuses the commits that would leave.)

The nature is part of the key — `.../gate/<name>` — because a gate and a context
may share a bare name. Keep the sibling prose that records why the gate exists.
