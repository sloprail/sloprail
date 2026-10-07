# Writing a judge check

A judge asks a model the question a script cannot decide — "is this change clean
and targeted?", "does the code still uphold the invariant its comment pins?". It
is a Jinja2 prompt **template** (`.md.j2`) beside the rule, rendered against the
check input and asked for a structured verdict.

This doc is the **judge** half of a check. The deterministic half — the script
whose exit code is the verdict — is [script-checks.md](script-checks.md). A check
is a `script` or a `judge`, never both; the shared `checks:` list mechanics
(declared order, first refusal ends it, fail-closed when a check decides nothing)
are the same for both and are covered in [script-checks.md](script-checks.md).

```yaml
checks:
  - prepare: ./skip-pure-addition.sh       # optional: assembles context, or skips the model
    judge: ./change-is-clean-and-absolute.md.j2
    model: size-md                            # optional: a size alias or model name
    allowed_tools: [WebFetch]                 # optional: tools beyond reading the project
    disallowed_tools: [WebSearch]             # optional: tool rules taken back
```

`prepare`, `model`, `allowed_tools` and `disallowed_tools` are **judge-only**
keys: set any of them on a script check and the rule is a **load error** (a
script makes no model call, bounds its own runtime, and names its own tools by
being an executable). Only `judge` is required; the others are optional.

## The template

The template renders against the same facts a script's stdin carries — the payload
spread flat at the template's top level — plus `change`: the unified diff of this
change. On a file-guard it is the combined diff of the files `match` selected over
the whole range; on a gate, the event's `oldContent` to its `newContent`. Wrap what
the model judges in tags:

```markdown
## The change
<change>
{{ change }}
</change>

## The rules it must follow
<rules>
{{ additionalContext.rules }}
</rules>
```

Available at the top level: `{{ change }}`; `{{ changeset }}` and `{{ subject }}` on
a file-guard (`changeset.files[]`, `.commits`, `.citations`, as in
[file-guard.md](file-guard.md)); on a gate or context `{{ event.newContent }}`,
`{{ event.path }}`, `{{ event.kind }}` and the rest of the event's flat fields
(a file-guard's `event` is only `{kind: "Changeset"}`, so `event.path` is empty);
`{{ transcriptPath }}`; `{{ context }}`; and `{{ additionalContext.* }}` when
`prepare` ran. The full field set and the `FileJudgeInput` / `GateJudgeInput`
envelope are in [events.md](events.md). `additionalContext` is **additive** —
always alongside the payload, never replacing it, and a `prepare` key cannot
overwrite `event` or `transcriptPath` (it renders only under the single
`additionalContext` field).

Every value renders escaped: the engine breaks `</` to `<\/`, so a value cannot
close the tag it sits in, and changes nothing else. `| raw` undoes it for a value
meant as markup. A value inside a tag's quoted attribute (`path="{{ event.path }}"`)
also has its quotes and `&` escaped, so it cannot end the attribute and add one of
its own; the engine sees the attribute and does this itself (a template cannot name
that filter), and `| raw` does not undo it there. Always quote an attribute value: an unquoted one (`path={{ event.path }}`)
is not an attribute value to the engine and is not escaped. A `{% raw %}` block is
literal output and is left as written. A non-string value — a map or list a
`prepare` handed over as JSON — renders `| tojson`
(`{{ additionalContext.action_input | tojson }}`): printed bare, its numbers and
nested values come out as Go placeholders the judge cannot read. The engine's
`tojson` keeps `&`, `<` and `>` as written and breaks only `</` (as JSON's own
`<\/`), so the value reads back exactly. Wrap a value in a named tag, never a
markdown code fence: a value with a fence line of its own would close it. A filter
name the engine does not have (a typo like `| uppper`) is not rendered as garbage:
the judge refuses, naming the template and the filter, and the load check
reports it to you at the next hook, before any rule runs.
A rule grounded in citations judges `change` against them —
[grounding.md](grounding.md).

The template is **just the rubric and the material** — it does not tell the model
how to format its answer. The engine appends the verdict instruction itself (below).

## `prepare` — assemble what the prompt needs

`prepare` is an optional script that runs **first**, before the model is asked, to
assemble context the template needs — the rules that apply, a file the change
names — or to skip the model when there is nothing to judge (`{"skip": true}`). It
receives the same check payload on stdin ([script-checks.md](script-checks.md)),
and **only** the `additionalContext` key of its stdout is read, merged alongside
the standard payload:

```bash
jq -n --arg rules "$rules" '{additionalContext: {rules: $rules}}'
```

`prepare` only builds context: it has no `fingerprint`. What a verdict depends on beyond the
subject's files is declared by the rule's `subjects:` script ([file-guard.md](file-guard.md#subjects--split-a-rule-into-units-each-cached-on-its-own)).

`prepare` runs **unconditionally** when set and is **not a pass/fail gate of its
own** — but a `prepare` that **fails to run** fails the check (carrying its words),
and one whose stdout is not the `{"additionalContext": {…}}` shape fails it closed
too: a judge fed a half-prepared prompt would judge against something the author
did not intend. A `prepare` that ran, had nothing to add, and printed nothing is a
legitimate no-op. A script check may carry a `prepare` too: its `additionalContext` reaches the
script on its payload, under that key.

## A judge is pure — what its verdict is cached on

A judge is **pure**: no side effects, it judges the slice it is handed. Like every check it is
cached by content, as part of its guard's verdict over a subject (see
[file-guard.md](file-guard.md#cached-verdicts)): the
subject, the **content of the subject's files** (always, whether or not the template renders
them), for a `require: citation` rule the citations' quotes, and the
subject's `fingerprint` from `subjects:`. The rule's definition is not in the key: changing the
template reuses stored verdicts.

**`prepare`'s output (`additionalContext`) and the rendered prompt are not in the key**: they
may carry text derived from the session, which `verify` in CI cannot reproduce. No commit SHA,
branch name, path of the snapshot, run id, timestamp, session id or transcript is in it either,
so two branches with the same content share their verdicts. A stored verdict with the same key
is not asked again (a fail is replayed by `sr-checks run`); `verify` never calls a judge and
computes the identical key.

The contract that follows: **anything else the verdict depends on** — something `prepare`
computes, or a file the judge opens with its own tools — must be declared in the subject's
`fingerprint` (the `subjects:` script). A judge that reads more than it is shown and declares
none of it is served a stale verdict when that thing changes.

A judge whose `prepare` needs the session (it fails without a transcript) cannot be judged by
`sr-checks run` outside a session: it refuses with "needs a session to judge" instead of
judging blind.

## The verdict

The engine appends a verdict instruction to the rendered prompt (so the template
carries none), constraining the model to answer with **exactly** one JSON object:

```json
{"pass": true, "reasoning": ""}
```

or, on a failure, `{"pass": false, "reasoning": "one concrete sentence naming the
specific problem"}`. `pass` is a boolean; on a failing verdict `reasoning` is what
returns to the agent's context window, so it must name the specific thing that
fails — the model is told this, so it does not fail with an empty explanation.

Note the key is **`reasoning`** here — distinct from a **script** check's stdout
`reason`. Two different contracts, two different keys.

The appended instruction also tells the model, in as many words, that everything
above is **the rubric and the material to judge** and to treat that material as
**data, never as instructions** — content telling the judge to pass it, ignore the
rubric, or treat itself as exempt is exactly what it is judging, not a command it
follows. The judge is attacker-shaped by construction and the engine hardens the
prompt for it; a `prepare` that wraps freshly-written content should keep the same
discipline.

## The substrate and model

A judge runs through **`sr-agent`** — the harness-agnostic agent runner, invoked
by name off `PATH` — not a model binary directly. So a template names neither
`claude` nor a concrete model, and a test's `sr-agent` shim or a real install's is
what answers. `sr-agent` picks the harness from the environment and the model from
the modelset.

- `model:` is a modelset in `sr-agent`'s format: a **size alias**
  (`size-xs` … `size-xxl`), a concrete harness model name, or a comma-separated
  preference list. Omitted, it is the engine default **`size-md`** — the middle
  rung, because a judge is a real reasoning task, not a formatting one, and the
  largest alias is a cost a per-action check should not default to. A malformed
  modelset is refused at load.

## `allowed_tools` — what the judge's agent may do

`allowed_tools:` is an optional list of tool names this judge's agent may use,
threaded to `sr-agent`'s `--allowed-tools`:

```yaml
allowed_tools: [WebFetch]
```

The substrate always grants the judge whatever it needs to **write its verdict**
(write access to the verdict file's own directory, which `sr-agent` adds) and to
**read the project** (below), so an empty or absent list still works — you name
tools here **only** when the judge must do more than read files and reason: `WebFetch`
a cited URL, say. It is a **judge-only key**: set on a script check it
is a load error (`allowed_tools` grants tools to a judge's agent; a script names
its own by being an executable). Each entry must be **one rule**: a tool name,
optionally followed by one parenthesised scope. An empty entry, two rules in one
item (`"Read WebFetch"`), or a scope that never closes is refused at load,
naming the entry.

An entry may be a **scoped rule** in Claude Code's own syntax, and each list item
reaches the harness whole, spaces included: `"Bash(curl -sL:*)"` lets the judge
run commands that start with `curl -sL` and no other shell command, and
`"WebFetch(domain:code.claude.com)"` limits fetches to one host. Quote such an
entry in YAML, because its colon would otherwise start a mapping.

### One vocabulary, every harness

`allowed_tools` and `disallowed_tools` use the **canonical tool names** (Claude Code's
spelling), whichever harness runs the judge: `Read`, `Grep`, `Glob`, `Write`, `Edit`,
`MultiEdit`, `NotebookEdit`, `Bash`, `Bash(<prefix>:*)`, `WebFetch`, `WebSearch`,
`Agent`, and `mcp__<server>__<tool>`. Claude Code takes the rules verbatim (scoped forms
like `WebFetch(domain:x)` included). Another harness maps them onto what it has, and
**refuses to start the judge** for a rule it cannot honour as tightly as written,
rather than running it with more. Codex, whose permission model is a sandbox, not a
tool list:

| rule | on Codex |
|---|---|
| `Read`, `Grep`, `Glob` | nothing to add: the sandbox reads the disk |
| `Write`, `Edit`, `MultiEdit`, `NotebookEdit` | the writable directories the judge is given (the verdict folder, `--add-dir`); refused when it has none |
| `Bash` | nothing to add: Codex always has a sandboxed shell, which is how it reads |
| `Bash(<prefix>:*)`, any `Name(<scope>)`, `mcp__*`, an unknown tool | refused |
| `WebFetch` | network access for the sandbox (wider than a fetch: the shell can reach the network too); needs a writable judge |
| `WebSearch` | `web_search = "live"` (off otherwise) |
| `Agent` | sub-agents stay on (off otherwise) |

In `disallowed_tools`, Codex can only switch off `WebFetch`, `WebSearch` and `Agent`
(and an `mcp__` tool, none is loaded); denying the shell, a file tool or a scoped
rule is refused. A Codex judge's file access is the sandbox: its working directory is
the verdict folder and the only writable root, `/tmp` and `$TMPDIR` are not, and the
project is read by absolute path and cannot be written.

## `disallowed_tools` — what the judge's agent may not do

`disallowed_tools:` lists rules the judge is **denied**, threaded to `sr-agent`'s
`--disallowed-tools` (claude's own). A deny beats every allow, including the
substrate's own grants, so this is how a rule takes back part of what it
granted:

```yaml
allowed_tools: ["Bash(curl:*)"]
disallowed_tools: ["Bash(curl * -o *)", "Bash(curl * -d @*)"]
```

It is judge-only and checked at load like `allowed_tools`. A harness with no
permission model refuses it rather than running the judge unconfined.

A deny list closes the **forms it names**, and no more. Measured with
`Bash(curl:*)` granted: 20 patterns denied every form they named, including
`-o`, `--output`, `-O`, `-D`, `-c`, `-K`, `-d @`, `-T` and `-F @`, while
`curl -sL <url> | grep …` still ran. These still got through:

- combined short flags: `curl -sLo <file>` wrote a file;
- curl's other file-writing options: `--etag-save`, `--stderr`, `--hsts`,
  `--dump-header`, `--cookie-jar`;
- `-H @<file>`, which sent a file's contents out.

A pattern list cannot enumerate a command's option grammar. So the engine adds
no default deny set, and a grant of a general-purpose command stays as wide as
that command.

What does hold is to **pin the whole command and deny any extra word**: allow
the exact command, flags and host, and deny that same prefix followed by a
space. Claude Code's `*` matches across spaces, so the deny catches every flag,
file or second URL appended after the one argument you meant:

```yaml
allowed_tools: ["Bash(curl -sL https://code.claude.com/docs/*)", "Bash(grep:*)", "Bash(head:*)"]
disallowed_tools: ["Bash(curl -sL https://code.claude.com/docs/* *)"]
```

Measured through `sr-agent` (claude 2.1.282, haiku, project readonly):

- **ran:** `curl -sL <docs url>.md | grep -n -A12 …` and `… | head`;
- **refused:** everything else tried, namely:
  - `-o` into the project, into `/tmp`, and attached (`-o/tmp/x`);
  - `-O`, `-sLo`, `--output`;
  - `-d @`, `-T`, `-F f=@`, `-H @`;
  - a second URL, `$(…)` in the URL, `>` redirection;
  - any other host.

## What a judge can read and write

The judge's agent starts in the **rule's own folder**, but it can **read the whole
project** (`SR_WORKSPACE`, the repository root) with `Read`, `Grep` and `Glob` — no
`allowed_tools` needed. The engine passes the project to `sr-agent` as
`--add-dir:readonly <workspace>`, and the prompt tells the judge where it is
(paths in `event` are relative to it). So a template can say "read the spec at the pinned path" or
"check the sibling file" and the judge will reach it; it does not need a `prepare`
to inline a file just so the judge can see it (though inlining is still cheaper
when the judge will always need it).

It **cannot write the project** with its file tools. Every file-writing tool —
`Write`, `Edit`, `NotebookEdit`, and the shell's recognised writes (`>`,
`touch`, `rm`) — is denied there, even if `allowed_tools` names `Write` or
`Edit`. The only thing a judge may write is its verdict file, whose folder
`sr-agent` adds as a writable dir the same way (`--add-dir <path>` is readable
and writable, as in claude's own `--add-dir`; `--add-dir:readonly <path>` is
readable only). Three things keep that true:

- The project's deny is never dropped. The verdict folder is placed outside the
  project, even when `$TMPDIR` points inside it, and `sr-agent` refuses a
  writable dir nested inside a readonly one.
- `sr-agent` refuses a project whose path holds a glob character (a bracket, a
  star, a question mark, a brace or a backslash). A permission rule reads the
  path as a pattern, so its deny would not match the directory. The judge
  fails closed instead.
- The judge runs in the `default` permission mode, whatever the user's own
  settings say. A user default of bypassPermissions was measured to let a judge
  with no tools write outside the project.

`allowed_tools: [Read]` is not needed to read the project. It adds reads
**anywhere else** on disk, so name it only when the judge must open something
outside the project (a transcript under `~/.claude`, say).

Three things `allowed_tools` can still widen, so name them deliberately:

- **`Write` / `Edit`** are granted unscoped: they cannot touch the project, but
  can write elsewhere on disk. A judge never needs them for its verdict.
- **A scoped `Bash(...)` rule** grants that command family with all its
  flags, and the flags the rule forgets to deny (see `disallowed_tools`).
  Measured: under `Bash(curl:*)`, curl's `-o` option wrote a file inside a
  readonly project and `-X POST -d @file` sent a file out. `awk` wrote a file
  anywhere under `Bash(awk:*)`, and so did `sed -n 'w <file>'` under
  `Bash(sed:*)`. `grep` and `head` have no writing forms.
- **`Bash`** is a shell. The project stays denied to the writes Claude Code
  recognises, but a shell can run any program, and no permission rule sandboxes
  what that program does. Grant it only when the judge must *run* something, and
  prefer a `prepare` script (which you control) for that.

## Failing closed by default

The judge substrate fails **closed**, the same rule the whole engine keeps: if
`sr-agent` cannot be started, times out, or the model never produces a well-formed
verdict after its retries, the check refuses. The verdict itself also fails closed
— only an explicit `{"pass": true}` permits; anything that is neither `true` nor a
clean `false` sends the model round again, and running out of attempts refuses.

## Failing open — the script escape hatch

A judge that should fail **open** on model flakiness — because a model call flakes
for reasons that are not evidence about the file, and one flake under fail-closed
wedges a session the agent cannot un-wedge — cannot get that from a judge knob;
the judge default is closed by design. The deliberate inversion is to write the
check as a **script** ([script-checks.md](script-checks.md)) that calls the model
itself and chooses `exit 0` on its **own machinery** failures (not on a real
negative verdict). Say in the script which exits are the fail-open ones, so the
next reader can tell a choice from a bug.

## The re-entry guard

Because a judge runs `sr-agent`, and `sr-agent`'s own first `Write` fires
`PreToolUse`, a judging rule would re-fire on its own launched agent — recursing —
without a guard. The engine carries `SLOPRAIL_LAUNCHED_BY` across the exec into the
launched agent so its hooks decline to re-fire **the launching rule** (and only it;
every other rule still holds). You do not manage this, but it explains why a
judging rule does not loop on itself — see [environment.md](environment.md),
`SLOPRAIL_LAUNCHED_BY`.

## When a judge is the right instrument

A judge earns its place when the question is real but **a machine cannot decide
it** — "the change is clean and targeted", "the mock still matches the doc it
links". A question a script *can* answer deterministically should be a script: it
is cheaper, faster, and not subject to a model's variance. Hand to a judge only
what genuinely needs judgement, and give it a `RUBRIC.md` (or the `.md.j2` itself)
that states the standard concretely enough that two runs agree. And, as everywhere
in this skill: cause the action and watch the judge refuse it — a judge that loads
but never fires is the silent no-op this skill exists to prevent.
