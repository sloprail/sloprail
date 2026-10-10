# Events and their fields

A gate or a context runs on events: a file about to be written, a command about to run, a
turn ending. This page lists what each kind carries. The load check is the authority: when a
rule names a kind or a field the build does not have, it reports the rule and lists what
exists. A file-guard binds to no event; its checks get a changeset of commits, described in
[file-guard.md](file-guard.md#what-a-check-receives).

## How an event is shaped

A check reads the event under `.event`, its fields directly there: `.event.kind`,
`.event.path`. A `match` reads the same fields as `event.path` ([matchers.md](matchers.md)).
A field the kind declares but the event lacks holds its type's empty value (`""`, `[]`,
`false`), so `event.newContent == ""` is true for an emptied file rather than an error.

## Which kinds each nature can bind

| | file events before they happen, `PreCommandInvoke`, `PreToolUse` | `Stop` | file events after they land, `PostTagWrite` |
|---|---|---|---|
| gate | yes | yes | no |
| context | yes | no | yes |

A gate checks before something happens, so it never binds to an event about what already
did. A context never binds to `Stop` because its `exit` already runs there. A rule naming a
kind its nature does not admit fails to load.

## File events

One event per file. `path` is relative to the repository root.

| kind | fields |
|---|---|
| `PreFileCreate` | `path`, `newContent`, `resultKnown`, `newMarkers`, `citations` |
| `PreFileUpdate` | `path`, `oldContent`, `newContent`, `resultKnown`, `oldMarkers`, `newMarkers`, `citations` |
| `PreFileDelete` | `path`, `oldContent`, `oldContentKnown`, `oldMarkers`, `citations` |
| `PostFileCreate` | `path`, `newContent`, `newContentKnown`, `newMarkers`, `seen`, `citations` |
| `PostFileUpdate` | `path`, `oldContent`, `newContent`, `newContentKnown`, `oldMarkers`, `newMarkers`, `seen`, `citations` |
| `PostFileDelete` | `path`, `oldContent`, `oldMarkers`, `seen`, `citations` |

In a trigger, `PreFileWrite` stands for `PreFileCreate` and `PreFileUpdate` together, and
`PostFileWrite` for the two `Post` kinds. A delete is never part of a write.

- `oldContent` is the file before the change; `newContent` is what the change leaves.
- `resultKnown`: see [gate.md](gate.md#an-unknown-result-is-yours-to-refuse).
- `oldContentKnown` and `newContentKnown` are `false` when the file was not read (too large,
  or not a regular file); the content is then `""`.
- `newMarkers` and `oldMarkers` are the `sr:` markers in the text, a list of
  `{kind, fqn, line}`. Write markers with `sr-mark` (see its `--help`).
- `citations` are the user's words or tool outputs the change was made with
  ([grounding.md](grounding.md)), each `{quote, sourceTypes, path, line, message, call}`.
- `seen` is explained [below](#seen-already-shown-to-an-earlier-stop).

## `PreCommandInvoke`: a command line about to run

| field | |
|---|---|
| `raw` | the command line as written |
| `invocations` | every program the line runs, flattened |
| `citations` | as on a file event |

A pipeline, an `&&` chain, a subshell, `sudo` or `xargs` each nest programs inside one line.
`invocations` lists all of them flat, so a rule matches the list and nesting cannot hide a
program from it. Each invocation has:

- `.bin`, the program, and `.argv`, its arguments;
- `.flags`, a map from flag name to the list of values given (`--tag=a --tag=b` is
  `["a", "b"]`; a flag without `=value` is `[""]`; a flag not given is `[]`). Flag names are
  not checked when the rule loads ([matchers.md](matchers.md#reads-the-loader-cannot-check));
- `.cwd`, the directory it runs in as far as the line says (`"."` is where the line started,
  `""` when only running something would tell);
- `.env`, the variables the line itself sets for it (`GIT_DIR=x git …`);
- `.stdin` and `.stdinKnown`, the literal text a heredoc or here-string feeds it;
- `.gaps`, the positions in `.argv` where a word could not be resolved (an unset `$VAR`, a
  `$(…)`), so a rule can refuse rather than guess; and `.gitGapEarly`, `true` when that
  happens where a `git` subcommand stands, so the command could be any git command.

What only running the line would reveal, such as a program named by a variable, is left out.
This is an aid to writing rules, not a security boundary. A command has no `Post` event: what
it changed arrives as file events.

## `PreToolUse`: a tool call about to run

| field | |
|---|---|
| `tool` | the tool's name, the same on every harness: `Bash`, `Write`, `Edit`, `Read`, `Grep`, `Glob`, `WebFetch`, `WebSearch`, `Agent`, `mcp__<server>__<tool>` |
| `nativeTool` | the name the harness itself used |
| `input` | the tool's arguments, an open map (`input.file_path`) |

Use it for what no file or command event covers: an MCP call, a web fetch.

## `PostTagWrite`: the tags the agent wrote

`tags` is every distinct `#tag` the agent wrote in its replies this cycle, each `{label, seen}`,
with `label` the text after `#`. A context reacts to the whole set at once. A tag is `#`
at the start of the text or after a space, then a letter or `_`; `#42`, a `# heading`, and
anything in code or a quoted line are not tags.

```
any(event.tags, .label == "research")
any(event.tags, .label == "skip" and not .seen)
```

## `seen`: already shown to an earlier Stop

A turn's cycle stays open until a Stop passes. When a Stop is refused, the retry delivers the
same tags and changed files again, so a rule whose obligation must survive a refusal keeps
seeing it. `seen` is `true` on a tag or a file event an earlier Stop was already handed
unchanged; a rule about only what is new since the last Stop reads it.

## `Stop`: a cycle ended

`Stop` has no fields. A rule bound to it finds its subject elsewhere: what a context recorded
([state-management.md](state-management.md)), or the transcript.

## What a check reads on stdin

A gate's check gets the event, the transcript path and every declared context:

```json
{"event": {"kind": "PreFileCreate", "path": "memories/a.md", "newContent": "…", "newMarkers": []},
 "transcriptPath": "/abs/…/session.jsonl",
 "context": {"research-run": {"active": true, "payload": {}}}}
```

A context's `enter` and `exit` get more ([context.md](context.md#what-enter-and-exit-read)),
and a judge template reads the same fields by name ([judge-checks.md](judge-checks.md)).

## Past events

`sr-session trajectory normalize` returns earlier entries of the session with their events
under `.events[]`, shaped like the live `.event`, so a check reads a past command the way it
reads the current one:

```bash
sr-session trajectory normalize --path "$tp" --events PreCommandInvoke \
  | jq '[.[] | .events[] | .invocations[]? | select(.bin == "git")]'
```
