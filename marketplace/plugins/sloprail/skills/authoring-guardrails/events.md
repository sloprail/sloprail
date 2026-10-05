# The events a rule binds to

Every check and every match reads an **event**. This is the single reference for
what each event kind carries and how it is shaped on the wire — the other docs
point here rather than re-listing the fields. Ground truth is the engine
(`internal/declaration/events.go`, the module declarations under
`internal/{filemod,commandmod,tooluse,tagmod,cyclemod}`, `internal/guardrail/scopes.go`,
and the check payloads in `internal/declaration/payload.go`); this doc mirrors it,
and the load check is the copy to trust when the two disagree.

## The event is read FLAT

An event's own fields sit **directly under `event`** — `.event.path`,
`.event.newContent`, `.event.kind`, `.event.resultKnown`, `.event.invocations`,
`.event.tags`. There is **no** `.event.fields.*` nesting: that nested
`{kind, fields}` envelope is the OLD format's. Historical events from
`sr-session trajectory normalize` are flat the same way — see "Past events"
below.

A field the kind declares but the event omits is filled with its type's **zero
value** (an empty string, an empty list, `false`) — never dropped — so
`newContent == ""` fires rather than errors on an empty file, and
`len(newMarkers) == 0` holds on unmarked content. The one place that bites is a
`PreFileUpdate` whose result is underivable: an absent `newContent` reads as `""`,
indistinguishable from an emptied file, which is what `resultKnown` exists to tell
apart (see below).

## The kinds and their fields

The kind is the discriminator, carried alongside the fields as `.event.kind`.
Field types are the modules' own.

### File events

One file, one event. `path` is repository-relative and every file kind carries
it. `PreFileWrite` is an **alias** the engine expands to `PreFileCreate` +
`PreFileUpdate` (and `PostFileWrite` to the two Post kinds) — a shorthand in a
trigger's `on`, not a kind the engine emits.

| kind | fields |
|---|---|
| `PreFileCreate` | `path`, `newContent`, `resultKnown`, `newMarkers`, `citations` |
| `PreFileUpdate` | `path`, `oldContent`, `newContent`, `resultKnown`, `oldMarkers`, `newMarkers`, `citations` |
| `PreFileDelete` | `path`, `oldContent`, `oldContentKnown`, `oldMarkers`, `citations` |
| `PostFileCreate` | `path`, `newContent`, `newContentKnown`, `newMarkers`, `seen`, `citations` |
| `PostFileUpdate` | `path`, `oldContent`, `newContent`, `newContentKnown`, `oldMarkers`, `newMarkers`, `seen`, `citations` |
| `PostFileDelete` | `path`, `oldContent`, `oldMarkers`, `seen`, `citations` |

- `seen` — bool, Post kinds only. `true` when an earlier Stop was already
  handed this file with the same content: the event is a re-send, not a change
  since the previous Stop (see [Re-sent events: `seen`](#re-sent-events-seen)).
- `path` — string, repository-relative. Prepend `$SR_WORKSPACE`
  ([environment.md](environment.md)) to reach the file on disk.
- `oldContent` — string, the file's bytes **before** the change. On a `Pre`
  update/delete it is the file on disk; on a `Post` update/delete it is the
  session baseline (the prior bytes are no longer on disk). A **create has none** —
  nothing preceded it, and the create kinds do not declare it.
- `newContent` — string, what the write would leave behind: the created body on a
  create, the post-edit bytes on an update. On a `Pre` update it is meaningful
  **only alongside `resultKnown`**. A **delete has none** — nothing remains.
- `resultKnown` — bool, on `PreFileCreate` and `PreFileUpdate` **only**. Says
  whether the engine could compute `newContent`, or whether the value is a zero
  standing in for "the engine could not work it out". See the discipline below.
- `oldContentKnown` — bool, on `PreFileDelete` **only**. The same gap on the
  delete side: `false` when the delete is predicted without its bytes being read
  — the file is not a regular file (a link to a FIFO or a device), larger than a
  delete read takes (8 MiB), or does not fit in what is left of a recursive
  removal's byte budget (see
  [file-guard.md](file-guard.md), "Deleted files"). `oldContent` is then `""`.
  A rule that decides from what the file held reads `oldContentKnown` first; a
  rule about the path needs neither. Its `oldMarkers` are then those of the file's copy
  at HEAD when that is tracked and within the cap (so a marker-matched guard
  still selects it before the delete), and empty otherwise.
- `newContentKnown` — bool, on `PostFileCreate` and `PostFileUpdate` **only**.
  `false` when the settled file was not read: not a regular file once links are
  followed, or larger than one read takes (64 MiB). `newContent` is then `""`.
- `newMarkers` / `oldMarkers` — the `sr:` markers of the new / old text, each a
  **list of `{kind, fqn, line}`** (`kind` string, `fqn` string, `line` int).
  `newMarkers` is set on the create and update kinds (the markers `newContent`
  would carry); `oldMarkers` on the update and delete kinds (the markers the file
  carries now). A create has no `oldMarkers`; a delete has no `newMarkers`.

- `citations` — the citations the change was grounded in: a **list of
  `{quote, sourceTypes, path, line, message}`** (`sourceTypes` a list of `user` /
  `tool_result`, `path` the absolute transcript, `line` int, `message` the whole
  entry the quote came from, `call` the tool call behind a `tool_result`). Empty unless the
  change was made grounded — see [grounding.md](grounding.md).

Read a marker's quote off `.fqn`, and test a list with a quantifier:

```bash
quote="$(printf '%s' "$payload" \
  | jq -r '(.event.newMarkers // [])[] | select(.kind == "asked") | .fqn' | head -1)"
```

Full treatment of the create/update/delete distinction, the pending-bytes reads,
and markers in a file-guard's own **match scope** (where they appear as `markers`,
the file's markers after the change, and `oldMarkers`, before it — not
`newMarkers`): [file-guard.md](file-guard.md).

#### The resultKnown discipline

On a `PreFileUpdate` (and an underivable `PreFileCreate`, e.g. a `NotebookEdit`
whose `newContent` is one cell rather than the JSON document), an **absent
`newContent` reads as the empty string** — the same observation as a write that
empties the file. `resultKnown` is the boolean beside the value that tells the two
apart. So **guard on `resultKnown` before reading `newContent`**:

```
event.resultKnown and not (event.newContent contains "---")   a gate trigger: refuse a strip, say nothing where the engine cannot see
not event.resultKnown                                          a gate trigger: catch the underivable cases deliberately
```

In a script, check `resultKnown` first; a **gate that prevents** a write refuses
(exit 1) when the result is not known, because a write whose bytes nobody saw has
not been checked. A file-guard never meets this: it reads committed bytes from
`changeset.files[]`. `PreFileCreate` carries `resultKnown` too, so check it on both kinds.
Details are in [file-guard.md](file-guard.md) ("The resultKnown discipline").

### `PreCommandInvoke` — a shell command line about to run

| field | type |
|---|---|
| `raw` | string — the command line as written |
| `invocations` | list — every program the module parsed out of it, flattened |
| `citations` | list of `{quote, sourceTypes, path, line, message, call}` — see [Citations](#citations-a-grounded-action) |

One command line is rarely one program: a pipeline, an `&&` chain, a subshell, a
`sudo`, an `xargs` each nest invocations. The module walks that once and emits
**every invocation flattened**, so a rule matches the list rather than the raw
string and nesting one level deeper does not defeat it. Each invocation carries:

- `.bin` — string, the program name.
- `.argv` — list of strings, its argument vector.
- `.flags` — a map of parsed flags with **open keys and typed values**. A flag
  name belongs to the command, not the engine, so the keys are checked against
  nothing: a mistyped flag name is a flag the command never passed, and reads as
  absent. Cause the command and watch the rule fire before trusting a flags match.
  Each value is a **list** of every occurrence, in order — `--tag=a --tag=b` is
  `["a", "b"]`, a flag given once is a one-element list, and a valueless flag
  carries `""`. Only the inline `--flag=value` form carries a value; a separated
  `--flag value` is `[""]` with `value` left in `.argv`. A flag the command did
  not pass reads as an **empty list**, so every list idiom answers false without
  it rather than erroring. The values are declared, so a comparison a list can
  never satisfy (`.flags.tag == "next"`, `.flags.tag startsWith "n"`) is refused
  when the rule loads, with the error saying flag values are lists.

  | question | match |
  |---|---|
  | was `--access` passed at all | `"access" in .flags` or `len(.flags.access) > 0` |
  | was it given a non-empty value | `any(.flags.access, # != "")` |
  | was `--tag=next` among them | `"next" in .flags.tag` |
  | the first value | `"tag" in .flags and .flags.tag[0] == "next"` (indexing an empty list errors) |
  | was `--tag` NOT passed | `not ("tag" in .flags)` — `.flags.tag == nil`, `!= nil` and a `??` default are refused at load, since an absent flag reads as `[]`, never nil |
- `.cwd` — string, the directory the program runs in as far as the line says,
  threaded through every `cd` ahead of it (a subshell's `cd` stays inside the
  subshell), through a wrapper that changes directory (`env -C DIR` /
  `--chdir`, `sudo -D DIR` / `--chdir` — what it wraps runs in DIR), and through
  a literal `eval` payload (`eval 'cd /x'` moves what follows; the programs in
  the payload are reported too): `"."` is where the line started, `"sub/dir"`
  is relative to that, `"/abs"` is absolute, and `""` means the directory could
  not be known without running something (`cd "$DIR"`, `cd -`, `pushd`, `env
  -C "$D"`, or anything after an `eval` whose payload is not literal, such as
  `eval "$(ssh-agent -s)"`). A file event's path is resolved the same way, with
  one difference: after an unreadable `eval`, a relative write target is still
  reported where the line started — such an eval almost never moves the shell,
  and dropping the target would hide the write from every file rule. Where the line started is the
  harness's working directory for that tool call — in a transcript, the record's
  own `cwd` — so a script joins a relative `.cwd` onto that.

- `.gaps` — list of integers, where `.argv` lost a word: `i` means a word of the line stood
  immediately before `.argv[i]` and could not be resolved, `len(.argv)` that one trailed the vector.
  Words are read against the shell variables the line assigned to literals earlier (`D=/x; ...`,
  `export D=/x && ...`) and nothing else: an unset variable, one from the real environment, a
  `${X:-default}` or a `$(...)` is dropped from `.argv` rather than read as empty. `-C $D push` is
  `["git", "-C", "push"]` with `.gaps == [2]`, so a rule that resolves a path from `.argv` can tell
  that a word is missing and fail closed. Empty when every word resolved.

- `.gitGapEarly` — bool, `true` only for a `git` invocation that lost a word among its global
  options or where its subcommand stands (`git $X`, `git -C $D push`, `timeout $T git push`), so
  the command that runs could be any git command. `false` for a gap after the subcommand
  (`git rev-parse "$REF"`), for no gap, and for every other program. A rule that must refuse a
  hidden git subcommand matches on it instead of on `.gaps`.

- `.env` — map of string to string, the environment the line itself sets for the
  program: an assignment prefix (`GIT_DIR=x git ...`), a wrapper's assignments
  (`env GIT_DIR=x git ...`, `sudo FOO=1 cmd`), and an `export NAME=v` earlier on
  the line in the same shell scope (a subshell's export stays in the subshell; an
  `if` branch that exports on one path only still names the variable, with value
  `""`). The value is `""` when it is not a literal word (`FOO=$X`,
  `FOO=$(cmd)`): the name is certain, the value is not. A program with nothing set
  has an empty map. It is only what the text says, not the environment the harness
  started with. Read it instead of grepping `.raw` for `GIT_DIR`: a commit message
  that merely mentions `GIT_DIR` sets nothing.

- `.stdin` and `.stdinKnown` — the text the line itself feeds the program: the body of a
  heredoc (`<<EOF`, `<<'EOF'`, `<<-EOF`) or the word of a here-string (`<<<word`, newline
  appended) attached to that command. `.stdinKnown` is `true` only when that text is literal;
  then `.stdin` holds it. Otherwise (a pipe from another command, `< file`, a variable or
  substitution in an unquoted heredoc body, no redirect at all) `.stdinKnown` is `false` and
  `.stdin` is `""`: the message is unknowable, so a rule that needs it should let the command
  through and leave the check to Stop and CI. `git commit -F -` is the case it exists for.

```
"GIT_DIR" in .env
any(event.invocations, .bin == "git" and "GIT_DIR" in .env)
any(event.invocations, .bin == "curl")
any(event.invocations, .bin == "rm" and any(.argv, # == "-rf"))
any(event.invocations, .bin == "npm" and "next" in .flags.tag)
len(event.invocations) > 1
```

In a script: `.flags.tag[0]` for the first value, `.flags.tag[-1]` for the last,
`(.flags.tag // []) | join(" ")` for all of them.

`.bin`, `.argv`, `.gaps`, `.gitGapEarly`, `.cwd`, `.env`, `.stdin` and `.stdinKnown` have declared shapes, so a mistyped key inside a
predicate is refused at load; `.flags` is the one map whose keys are open. Only what the parser
can see without running the command is emitted — a program named by a variable, a
decoded-and-piped payload — is left alone rather than guessed, so this is a
correctness aid, **never a security boundary**. There is no `Post` counterpart: a
command that ran shows its consequences as the file events. See [gate.md](gate.md).

### Citations: a grounded action

`citations` (on every file kind and on `PreCommandInvoke`) lists the citations
the action was made with, each `{quote, sourceTypes, path, line, message, call}`:
the quote, the pool(s) it resolved in (`user`, `tool_result`), the absolute
transcript, the line, the whole entry it came from (capped at 16KB), and — for a
`tool_result` — the tool call that produced it. The engine resolves every quote against the session's record before
any rule runs, so an entry always names a real entry of the record. An action
that cited nothing carries an empty list. A `Post` file event carries the
citations recorded for its path at pre-tool. How an agent cites, and how a rule
requires or judges a citation: [grounding.md](grounding.md).

### `PreToolUse` — a tool call about to run

| field | type |
|---|---|
| `tool` | string — the tool's name as the harness reports it (`tool == "WebFetch"`) |
| `input` | **open map** — the tool's arguments, shape is the tool's own |

The harness-native pre-action moment, for gating a tool no file or command event
covers (an MCP call, a web fetch, a bespoke tool) or activating a context from
"some tool ran" plus the trajectory. `input` is left open like `.flags`: a matcher
reading `input.file_path` reaches in unchecked. A tool with no arguments still
produces the event with an empty `input`, so a rule narrowing on `tool` alone
sees it. No `Post` counterpart — what a tool changed is the file module's `Post`
events.

### `PostTagWrite` — the `#tags` the agent wrote this cycle

| field | type |
|---|---|
| `tags` | list of `{label, seen}` — every distinct tag written this cycle, in order |

A bulk event, not one per tag: a message often holds `#update #decision`, and a
context deciding whether **its** tag showed up should see the whole set at once.
`.label` is the tag's text **without** the leading `#` (`#no-slop` → `no-slop`).
Duplicates are dropped, first-occurrence order kept. An empty `tags` is a real
answer ("nothing tagged this cycle") a context can react to. A `Post` fact only —
there is nothing to scan before the agent writes.

What counts as a tag: `#` at the start of the text or after whitespace, then a
letter or `_`, then letters, digits, `_` and `-`. Markdown emphasis right before
the `#` is fine — `**#research summary:**`, `*#research*`, `_#research_`,
`__#research__` all give `research`. Not a tag: `#42` (an issue reference), `# `
(a heading), `foo#bar` or `foo**#bar**` (inside a word), `~~#research~~`, and
anything in a code span, a fenced block or a `>` quoted line — text the agent
shows is not text it declares.

`.seen` is `true` when the tag is only in text an earlier Stop already read — a
refused reply's text, re-sent with the retry. A tag the agent wrote again since
the previous Stop is `seen: false`.

```
any(event.tags, .label == "research")                  # said at any point this cycle
any(event.tags, .label == "skip" and not .seen)        # said in the reply being judged
```

### Re-sent events: `seen`

A cycle stays open until a Stop passes. When a Stop is refused, the retry's Post
events deliver again what the refused reply already did — its tags, and every
file still differing from the session's baseline (which stays in the difference,
and is delivered on every Stop, until committed). That is deliberate: a rule
whose obligation must survive a refusal (a context entered on `#research`) keeps
seeing it even if the retry does not repeat the tag.

A rule that judges only what happened **since the previous Stop** reads `seen`:

- on a **tag**, `seen: true` = only in text an earlier Stop read;
- on a **Post file event**, `seen: true` = same content an earlier Stop was handed.

`seen` on a Post file event is delivered to **contexts** (and anything else still
bound to `PostFile*`). A **file-guard** does not receive Post file events: it
judges commits. What replaces `seen` for it is the verdict cache — unchanged input is
never run again, whatever the check; see [file-guard.md](file-guard.md#cached-verdicts).

"Earlier Stop" means the previous Stop that ran the rules, whatever it decided. A
Stop let through un-judged at `stop_hook_block_cap`, or a turn interrupted before
any Stop, records nothing — what it covered is delivered unseen again, so it is
re-judged rather than skipped.

### `Changeset` — what a file-guard's checks receive

Not a kind a gate or a context binds to: a file-guard binds to no event, and its
checks are handed one `Changeset` per rule per range (per **subject**, below). `event` is always
`{"kind": "Changeset"}`; the `.event.path`, `.event.newContent` and
`.event.oldContent` of a file event do not exist for a file-guard — loop over
`.changeset.files[]`.

```json
{"event": {"kind": "Changeset"},
 "changeset": {
   "base": "…", "head": "…",
   "commits": [{"sha": "…", "subject": "…", "body": "…",
                "trailers": {"Sloprail-Cites-User": ["…"]}}],
   "files": [{"path": "…", "status": "M", "oldPath": "", "oldContent": "…",
              "newContent": "…", "oldMarkers": [], "newMarkers": [], "diff": "…"}],
   "others": [{"path": "README.md", "status": "M"}],
   "citations": [{"quote": "…", "sourceTypes": ["user"], "path": "…", "line": 3, "message": "…"}]},
 "subject": {"id": "changeset", "files": ["…"]},
 "transcriptPath": "…"}
```

- `base`, `head` — the range, as SHAs: the merge base of `--base` and `--head`, and
  `--head` ([file-guard.md](file-guard.md)).
- `commits` — every commit in the range, oldest first; `trailers` maps the
  trailer key in canonical case (`Sloprail-Cites-User`) to its values.
- `files` — the files `match` selected, in full. `status` is `A` (body in
  `newContent`), `M` (`oldContent` is the range's first bytes, `newContent` the
  last), `R` (`oldPath` set; a rename is not a deletion) or `D` (`oldContent` and
  `oldMarkers`, no `newContent`). `diff` is that file's part of the squashed diff.
- `others` — the rest of the range, `{path, status}` only.
- `citations` — the range's resolved `Sloprail-Cites-*` quotes
  ([grounding.md](grounding.md)); empty outside a session.
- `subject` — the unit being decided: `{id, files}`, where `files` are paths in
  `changeset.files`. The rest of the changeset stays in the payload as context. It
  depends on what is asking:
  - a **requirement** (`require:` and its `when`) is evaluated once per subject, and the
    default subject is **one selected file**: `{"id": "<path>", "files": ["<path>"]}`.
    A `when` script decides for `.subject.files` and reads the rest of the
    changeset only as context; the requirement applies to the files whose `when`
    applies.
  - a **check** (a script or a judge) has ONE subject by default, the whole
    changeset: `{"id": "changeset", "files": [<every selected file>]}`.

  A rule's `subjects:` script ([file-guard.md](file-guard.md#subjects--split-a-rule-into-units-each-cached-on-its-own))
  supplies the list instead, in this same shape plus an optional `fingerprint`. A gate's payload has no `subject`: it has `.event`.
  `transcriptPath` as everywhere.

### `Stop` — a work cycle ended

**No fields.** The end of a cycle is about the cycle, not one file, so a rule
bound to `Stop` has nothing on the event to narrow on and establishes its subject
another way — usually by reading a context's accumulated registry
([state-management.md](state-management.md)), or by asking `sr-session query` /
`sr-session trajectory` about the transcript. `Stop` marshals to `{"kind":"Stop"}`
— an object, never a null — so `.event.<anything>` is a clean miss, not an error.
Declaring the fields as empty is what makes a match naming `path` on `Stop`
refuse at load rather than evaluate against nothing.

## Which kinds a nature admits

A nature's `on:` may name only the kinds its nature admits; the loader **refuses
at load** otherwise, rather than binding to something that silently never fires.

| | pre-file / `PreCommandInvoke` / `PreToolUse` | `Stop` | `PostFile*` / `PostTagWrite` |
|---|---|---|---|
| **gate** | yes | yes | **no** |
| **context** | yes | **no** (its `exit` is always checked on Stop anyway) | yes |
| **file-guard** | binds to a range of commits by nature — names no kind at all (`deletions:` decides whether `D` entries reach it) | | |

Alias availability follows from the table: `PreFileWrite` is admitted on both a
gate and a context; `PostFileWrite` is **context-only** (a gate does not wake on
settled content).

## The payload envelope

The event never arrives bare — it is one field of a payload the check reads on
stdin (or a judge template renders against). The envelope differs by nature and by
which script it feeds; all of them carry the event flat under `event`. The Go
shapes are in `internal/declaration/payload.go`.

### CheckPayload — the base envelope (a file-guard's is a `Changeset`)

```json
{"event":{"kind":"PreFileCreate","path":"memories/a.md","newContent":"…","newMarkers":[]},
 "transcriptPath":"/abs/…session.jsonl"}
```

- `event` — the event, flat. A file-guard's check does not get this envelope: it
  gets a `Changeset` payload ([above](#changeset--what-a-file-guards-checks-receive)),
  with deleted files as `status: "D"` entries when `deletions:` is `include` or
  `only` ([file-guard.md](file-guard.md)).
- `transcriptPath` — the session record, for reading what the event does not carry
  (which human message grounds this write). Also on `$SR_TRANSCRIPT`.
- No `context`: a file-guard's payload does not carry session state.

### GateCheckPayload — a gate's script / prepare / judge

The same two keys plus `context` (every declared context by name,
`{active, payload}`, at parity with a gate's match scope), and `event` is any **gate** kind — a gate wakes on command,
tool and `Stop` events too, never a `Post` variant. A gate on a pre-write event
(`PreFileWrite`, `PreFileDelete`) is where a write or a delete is prevented, and
it is run **once per file** a call changes. `context` is carried at top
level so a gate's checks can read what an upstream context left behind.

### ContextEnterPayload — a context's `enter`

```json
{"event":{"kind":"PostTagWrite","tags":[{"label":"research","seen":false}]},
 "transcriptPath":"/abs/…session.jsonl",
 "gates":{"depth-check":{"status":"pass"}},
 "currentContext":{"active":false,"payload":{…}}}
```

- `event` — the trigger that fired, flat. A **context** kind (a `Post` variant is
  possible here).
- `transcriptPath` — the session record `enter` reads to recognise "now it's the
  time" and extract its payload.
- `gates` — every declared gate's most recent verdict by name, `{status}` where
  `.status` is `"pass"` / `"fail"`. Lets `enter` gate its own activation on
  another gate's verdict.
- `currentContext` — **this** context's own last `{active, payload}`, because
  `enter` runs on every trigger whether or not it was already active.

### ContextExitPayload — a context's `exit`

The same shape, but `event` is **always the `Stop`** that triggered the check
(`exit` is consulted on nothing else, so only `.event.kind` is meaningful).
`currentContext` is this context's own entry — `payload` is what `enter` produced.
`gates` is again every gate's verdict, which is how a thin `exit` mirrors a paired
gate:

```bash
status="$(printf '%s' "$input" | jq -r '.gates["depth-check"].status // "fail"')"
[ "$status" = "pass" ] && exit 0
exit 1
```

See [context.md](context.md) for `enter`/`exit` in full.

### The judge inputs — FileJudgeInput / GateJudgeInput

A judge renders against the payload **spread flat at the template's top level**
(`{{ event.newContent }}`, `{{ transcriptPath }}`, `{{ context }}`), plus
`additionalContext` as one more top-level field, present **only** when the check's
own `prepare` returned one:

```markdown
## The rules it must follow
<rules>
{{ additionalContext.rules }}
</rules>
```

A file-guard's judge renders `{{ changeset }}`, `{{ subject }}` and `{{ change }}`
(the combined diff of the files `match` selected) in place of a file event's
fields; a gate's judge also gets `{{ change }}`, the diff of the event's
`oldContent` to its `newContent` ([judge-checks.md](judge-checks.md)).

`additionalContext` is additive — it never replaces the payload, and a `prepare`
key cannot collide with `event` or `transcriptPath` (it renders under the single
`additionalContext` field). See [judge-checks.md](judge-checks.md).

## Past events

`sr-session trajectory normalize` re-derives each entry's events and returns them
under the entry's `.events[]`, **flat** — `kind` beside the event's own fields,
exactly the shape of the live `.event`. A check asking "did a real `git clone`
happen this run?" reads a past event the way it reads the one in front of it:

```bash
sr-session trajectory normalize --path "$tp" --events PreCommandInvoke \
  | jq '[.[] | .events[] | .invocations[]? | select(.bin == "git")]'
```
