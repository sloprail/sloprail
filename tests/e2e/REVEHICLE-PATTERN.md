# Re-vehicling shared e2e tests off the old GUARDRAIL.md format onto the new nature format

**Status: PROVEN on `session/015_refusal_outlives_baseline` (all 4 subtests green under the CI env).**
This note is the mechanical transformation for the fan-out to apply uniformly to the
remaining ~34 shared dirs (session/\*, revalidation/\*, subagent/\*, proxy/025).

## Why re-vehicle at all

~35 e2e dirs test SHARED engine machinery the NEW format still uses — baseline
movement, tree-diff, `readdOutstanding`, refusal-survival across a branch switch,
subagent own-cycle, revalidation fingerprinting. They install that machinery's rule
via an OLD-format guardrail (`e.Guardrail` → `.sloprail/guardrails/<name>/GUARDRAIL.md`,
`hooks: PostFileCreate: …`) and observe it fire through the OLD dispatch. The new
declaration store does NOT read `GUARDRAIL.md`, so once the old dispatch is deleted
those rules load nothing and the tests fail — the coverage of the shared machinery
would be lost. Re-vehicling makes each test install a NEW-format rule
(`e.FileGuard` / `e.Gate`) and observe the SAME behavior through the NEW dispatch, so
the coverage survives the deletion. Do NOT weaken any assertion — the re-vehicled
test must prove the SAME shared behavior.

## Decision: file-guard vs gate

Pick the vehicle from **what the test OBSERVES**, not from cosmetics:

| The test observes…                                                              | Vehicle                                    |
|---------------------------------------------------------------------------------|--------------------------------------------|
| an AFTER-the-write refusal / a file RE-FIRING next cycle until fixed             | **file-guard**, after-check (default)      |
| a PRE-write BLOCK (write never lands, denied at pre-tool)                        | a **gate** on `PreFileWrite` (`PreFileDelete` for deletes) |
| a Stop-CHECKPOINT gate: `require` + `checks`, subjectless Stop, a gate blocking the turn as a whole (completeness, "was an artifact produced") | **gate** |
| a PRE-action block keyed to an event kind (a command about to run, a tool)       | a **gate** trigger on that event kind      |

Refusal-survival, revalidation re-fire, baseline/tree-diff, subagent own-cycle over
a file → **file-guard** (these are file-state rules; 015 is the archetype). A test
about turn-level completeness or a subjectless Stop checkpoint → **gate**.

If the new format genuinely CANNOT express what a test observes (some old-dispatch-only
behavior with no file-guard/gate equivalent), STOP and report that test precisely — it
may need to stay old-format (blocking that part of the deletion) or is a real gap. Do
NOT invent a weaker assertion to force a green.

## The mechanical transformation (file-guard case, from 015)

### 1. Installer: `e.Guardrail` → `e.FileGuard`

```go
// OLD
e.Guardrail(proj, "watcher", refuseNamed, map[string]string{"judge.sh": judgeScript})
// NEW
e.FileGuard(proj, "watcher", refuseNamedGuard, map[string]string{"judge.sh": judgeScript})
```

`e.FileGuard` writes `.sloprail/file-guard/<name>/file-guard.yaml` + sibling scripts
(executable). `e.Gate` writes `.sloprail/gate/<name>/gate.yaml`. Both already exist in
the harness and already write the new format — nothing to add there.
**Do NOT remove `e.Guardrail`** — the not-yet-migrated dirs still use it; a LATER step
removes it once all are migrated.

### 2. Declaration: OLD hooks-YAML → NEW `file-guard.yaml`

```yaml
# OLD (GUARDRAIL.md frontmatter)
---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./judge.sh
  PostFileUpdate: { … same … }
  PostFileDelete: { … same … }
---
# prose
```

```yaml
# NEW (file-guard.yaml body — no frontmatter, no prose)
match: "**/*.md"
checks:
  - script: ./judge.sh
```

- `match:` replaces the per-kind hook bindings. It is EITHER a **bare glob** (a
  string with no whitespace/quote) OR a full **expression** over `FileMatchScope`
  (`path`, `markers`, `context`).
  - Glob grammar (`internal/guardrail/scopes.go` `globRegexp`): `**` crosses
    separators and **`**/` is an OPTIONAL leading dir** (`(?:.*/)?`), so
    `**/*.md` matches `bad-file.md` at the repo ROOT and `sub/deep/x.md` at any
    depth — the faithful stand-in for an old rule that fired on every `*.md` write
    regardless of the old kind split. `*` is one segment, `.` is a literal dot,
    `{a,b}` is NOT expanded (use an `or` expression if you need alternation).
  - The old fixture keyed its refusal on the event's `path` rather than on content
    appearing "anywhere in the payload"; `match` over `path` is the direct analogue.
  - A content/marker rule can match on `markers` (e.g. `any(markers, .kind == "invariant")`).
- A file-guard is ALWAYS an after-check: it fires at Stop on the settled Post event and
  re-fires next cycle until fixed. It never sees a Pre event, and `preventive:` is
  refused at load — a test that observes a PRE-write block installs a gate instead
  (see "Gate case" below).
- The three old kinds (`PostFileCreate/Update/Delete`) collapse into the ONE file-guard,
  which fires on whichever Post kind the change produced. A re-added outstanding refusal
  arrives as **`PostFileUpdate`** (see 015 T015_04) — assert that kind if the test does.
- A file-guard is NOT handed deletes by default (`deletions:` absent means `skip`). An
  old rule that bound `PostFileDelete` — or a test that observes a delete through a
  guard's ledger — adds `deletions: include` (creates, updates and deletes) or
  `deletions: only` (deletes alone).

### 3. Script: OLD nested wire form + `$PWD` ledger → FLAT payload + `$SR_GUARDRAIL_DIR` ledger

Wire form (what the check reads on stdin):

| OLD (nested envelope)      | NEW (flat under `event`) |
|----------------------------|--------------------------|
| `.event.fields.path`       | `.event.path`            |
| `.event.fields.kind` / `.event.kind` | `.event.kind`  |
| `.event.fields.newContent` | `.event.newContent`      |
| `.event.fields.newMarkers` | `.event.newMarkers`      |

The event's own fields spread DIRECTLY under `event`, with `kind` alongside:
`{"path":"bad-file.md","newContent":"…","newMarkers":[…],"kind":"PostFileUpdate"}`.
A `sed -n 's/.*"path":"\([^"]*\)".*/\1/p'` still works because the flat form still
carries `"path":"…"`; only a JSON reader that walked `event.fields.path` must change to
`event.path`.

Refusal contract (`internal/dispatch/exec.go` `scriptRefusalReason`):

| OLD hook                              | NEW file-guard/gate check                            |
|---------------------------------------|------------------------------------------------------|
| exit 2, reason on **stderr**          | exit **non-zero**, reason as `{"reason":"…"}` on **stdout** (preferred; plain stdout/stderr are fallbacks) |
| exit 0 = permit                       | exit 0 = permit                                      |

Ledger location:

| OLD                                   | NEW                                                  |
|---------------------------------------|------------------------------------------------------|
| `$PWD/seen` (hook `$PWD` = `.sloprail/guardrails/<name>/`) | `$SR_GUARDRAIL_DIR/seen` (engine-set to `.sloprail/file-guard/<name>/`) |

The new CheckPayload has NO `guardrailDir` field. Where an old script derived the
project root from the payload's `guardrailDir`, derive it from the env instead:
`root="${SR_GUARDRAIL_DIR%/.sloprail/file-guard/*}"`.

The old fixture's leading `case "$path" in .sloprail/*) exit 0 ;; esac` skip was
LOAD-BEARING there (the ledger lived under `.sloprail/` and matched the old broad
binding, so re-observing it doubled the ledger every cycle). Under a `**/*.md` match it
is DEFENSIVE only — `seen`/`refused`/`file-guard.yaml`/`judge.sh` have no `.md` suffix
so the guard cannot re-observe its own bookkeeping — but keeping it is free and keeps
the intent legible. (If your `match` is broad enough to catch the ledger, the skip is
load-bearing again — prefer a `match` that doesn't.)

015's script, before/after:

```sh
# OLD
path="$(… sed … )"                       # from .event.fields.path
printf '%s\n' "$payload" >> "$PWD/seen"
case "$path" in bad*) echo "…" >&2; exit 2 ;; esac

# NEW
path="$(… sed … )"                       # from .event.path (flat) — same sed
printf '%s\n' "$payload" >> "$SR_GUARDRAIL_DIR/seen"
case "$path" in bad*) echo '{"reason":"…"}'; exit 1 ;; esac
```

### 4. Observation channel

| OLD                                                       | NEW                                                              |
|-----------------------------------------------------------|------------------------------------------------------------------|
| `e.Ledger(proj, name, file)` (`.sloprail/guardrails/<name>/<file>`) | `e.FileGuardLedgerLines(proj, name, file)` (`.sloprail/file-guard/<name>/<file>`) — **added by this wave**, the lines analogue of `e.Ledger` |
| parse `.event.fields.path` in the ledger lines            | parse `.event.path` (FLAT)                                       |
| `feature:.sloprail/guardrails/<name>/GUARDRAIL.md` (git path checks) | `feature:.sloprail/file-guard/<name>/file-guard.yaml`   |

Already-present harness helpers you can reuse:
- `e.FileGuardLedger(proj, name, file) int` — how MANY times the check ran (count).
- `e.FileGuardLedgerLines(proj, name, file) []string` — WHAT each run recorded (lines);
  use this when the test parses the payload back (015 does, for `.event.path`).
- `e.GateState(proj, sess, gateName) string` — a gate's recorded `pass|fail` verdict.

**Format-neutral — DO NOT change these:** `e.BlockingErrors` / `e.BlockingErrorsFrom`
read refusals out of the conversation record (`hook_blocking_error` attachments) and
work identically for old and new dispatch. `res.Refused()` / `res.Saw()` read the
pre-tool deny marker off the stream, also format-neutral. A test that only asserts a
Stop block via `BlockingErrorsFrom(proj, sess, "Stop")` needs its INSTALLER and
DECLARATION re-vehicled but keeps that assertion verbatim.

## Gate case (when the decision table sends you to a gate)

Use `e.Gate(proj, name, gateYAML, files)` → `.sloprail/gate/<name>/gate.yaml`. A gate's
check receives `GateCheckPayload` — same flat `.event.*` wire form, but its `event` is a
gate event kind (command / tool / Stop / `PreFileCreate` / `PreFileUpdate` /
`PreFileDelete`), never a Post file variant.

A PRE-write block is a gate triggered on `PreFileWrite` (= create + update):

```yaml
on:
  - event: PreFileWrite
    match: 'event.path startsWith "src/"'   # nested scope: event.path, event.newContent, event.resultKnown, …
checks:
  - script: ./refuse.sh
```

- No glob shorthand in a gate `match`: `docs/**` → `event.path startsWith "docs/"`,
  `**/*.md` → `event.path endsWith ".md"`, a middle wildcard → `event.path matches "^a/.*/x\\.md$"`.
- Deletes are a separate trigger (`- event: PreFileDelete`); an old `deletions: only`
  becomes a PreFileDelete-only gate.
- Marker matches read kind-specific fields (`newMarkers` on create/update, `oldMarkers`
  on update/delete), so use separate `PreFileCreate` / `PreFileUpdate` triggers instead of
  the `PreFileWrite` alias when the match reads them.
- A gate is asked about EVERY file a call changes, once per file event.
- A gate does NOT fail closed on an underivable write (`sed -i`, an unresolvable sr-file
  line): `event.resultKnown` is false and `newContent` is "". A content-dependent check
  must refuse on `resultKnown != true` itself.
- A pre-write block is observed on the tool-call channel (`res.Refused()` / `res.Saw()`),
  the refusal naming `(gate <name>)`; an after-check refusal is a Stop block naming
  `(file-guard <name>)`. Ledgers a gate's script writes go to `$SR_GUARDRAIL_DIR`, read
  with `e.GateLedgerLines(proj, name, file)`. Observe the verdict
with `e.GateState`; observe a turn block with `e.BlockingErrorsFrom(…, "Stop")`. A gate
whose check calls a model uses a judge template + one of the `InstallJudgeClaude*` shims
(already in the harness). See `tests/e2e/harness/fileguard/034_*` for file-guard check idioms
and the gate e2e dirs for gate idioms.

## Gate (build/vet/gofmt + the CI-faithful test run)

```sh
go build ./... && go vet ./tests/... && gofmt -l tests/      # all clean
go test -count=1 ./tests/e2e/harness/session/<dir>/...              # green
```

No `env -u …` prefix is needed: the harness strips the enclosing Claude Code session's
variables (CLAUDECODE, CLAUDE_CODE_*, CLAUDE_PROJECT_DIR, CLAUDE_PLUGIN_ROOT, CLAUDE_CONFIG_DIR,
SLOPRAIL_*, SR_*) from every process it spawns (`harness.HostEnv()`), so a run from inside
a live session equals CI. Any new test helper that spawns a process starts from
`harness.HostEnv()`, never a bare `os.Environ()`. To prove a re-vehicled test is NON-VACUOUS, temporarily break its
`match` (e.g. `match: "nonexistent/**"`) and confirm the arrival assertion fails — 015
was validated this way.
