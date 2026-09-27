# Writing a match

A `match` narrows a rule to the occurrences it is about. It is an expression that
must evaluate to a **boolean**, over the variables its scope exposes — no
filesystem, no environment, no other events. Absent means "every occurrence".

A **misspelled declared field is caught at load**: the rule is refused by name
and session start prints what the scope carries. You need not defend against that
one — but see "the open reads" below for the ones that are *not* caught.

## The three scopes differ in what they expose

This is the one thing to get right, because a file-guard and a gate read the same
fact under different names.

### File-guard: the file's own facts, **bare**

A file-guard's `match` reasons about a settled file, so it sees the file's facts
directly:

| variable | type | |
|---|---|---|
| `path` | string | the file's repository-relative path |
| `markers` | list | the `sr:` markers the file carries, elements `{kind, fqn, line}` |
| `context` | map | every declared context, by name, `{active, payload}` |

```
path endsWith "SKILL.md"
any(markers, .kind == "invariant")
context["refactoring"].active and any(markers, .kind == "moved-from")
```

### Gate and context: the event, **nested under `event`**

A gate's and a context's `match` sees the fired **event** under `event`, plus the
context map:

| variable | type | |
|---|---|---|
| `event` | structure | the fired event's own fields, typed to the trigger's kind |
| `context` | map | every declared context, by name, `{active, payload}` |

```
event.path startsWith "memories/decisions/"
any(event.invocations, .bin == "curl")
any(event.tags, .label == "research")
not context["tag-declared"].active
```

`event`'s fields are exactly what the kind carries — `event.path` on a
`PreFileWrite` trigger, `event.invocations` on a `PreCommandInvoke` one,
`event.tags` on a `PostTagWrite` one. A name the kind does not declare
(`event.paht`) is refused at load.

**So: `path` bare in a file-guard, `event.path` in a gate or context.** Writing
`path` in a gate, or `event.path` in a file-guard, is the most common cross-nature
mistake.

Neither scope exposes a `gates` map — a gate's verdict is not readable from a
`match`. (A context's `enter`/`exit` *scripts* do receive `.gates` on stdin, but
that is the check payload, not the match scope.)

## Operators on a `string`

The shipped rules use the **word** forms of the boolean operators (`and`, `or`,
`not`); the symbol forms (`&&`, `||`, `!`) also parse, but match the examples and
prefer words.

| | |
|---|---|
| `startsWith` | `path startsWith "memories/"` |
| `endsWith` | `path endsWith ".md"` |
| `contains` | `path contains "/decisions/"` |
| `matches` | `path matches "^docs/[0-9]+-"` (regular expression) |
| `and` `or` `not` | `path startsWith "src/" and not (path endsWith "_test.go")` |
| `==` `!=` | `path == "README.md"` |

## A `list`

Read with `any`, `all`, `none` and `len`:

```
any(event.invocations, .bin == "curl")
not any(event.invocations, .bin == "npm")
len(event.invocations) > 1
any(markers, .kind == "invariant")
```

A `bool` field is used directly: `resultKnown and not (newContent contains "---")`.

Check the type printed beside each field — the groups are not interchangeable.

## The glob shorthand — file-guard only

A file-guard's `match` may be written as a **bare glob** instead of an
expression, for the common "this path" case:

```yaml
match: "**/*.md"
match: "**/tasks/*/*/ASK.md"
```

The two forms are told apart by shape alone: a string with **no whitespace and no
quotes** is a glob; anything containing a space or a quote is an expression. So
`**/*.md` is a glob, and `path endsWith ".md"` is an expression — quote the whole
value in YAML when it starts with `*` so the parser does not choke.

The glob grammar (anchored over the whole path, `/`-separated):

| | |
|---|---|
| `**` | any run of characters **including** `/` — crosses directories, so `memories/**/*.md` reaches any depth |
| `*` | any run **except** `/` — one path segment |
| `?` | any single character except `/` |
| `[...]` | a character class, passed through |
| `.` | a literal dot (the common extension case) |

Brace alternation `{a,b}` is **not** expanded — a brace is a literal. A rule that
needs alternation writes the expression form with `or`.

**Gates and contexts have no glob shorthand** — their scope is not "a file at a
path", so they always write the expression: `event.path startsWith "…"`. Even a
file-guard often wants the expression, to combine a path test with a marker or a
`resultKnown` guard.

## The open reads — not caught at load

Two places a `match` is checked against nothing, so a typo evaluates false forever
and the rule silently never fires. Neither is a load error; **cause the event and
watch it fire** before believing either.

- **`context[...]`.** Context names are project-defined, unknown when the scope is
  built, so the map is left open — `context["reserch-run"].active` (misspelled)
  reads as absent, not as an error. This is the run-time cost of not punishing an
  author for a name the engine cannot know.
- **A flag off an invocation.** `event.invocations[].flags` belongs to the command
  being run, not the engine, so a key read off it is verified against nothing. A
  mistyped or non-existent flag compiles and evaluates false. Its values are
  declared (a list of strings each), so `.flags.tag == "next"` is refused at
  load; `"next" in .flags.tag` is the match.

The **inside of a list is checked** where its element shape is declared:
`any(markers, .knid == …)` and `any(event.invocations, .bni == …)` are refused at
load with the real keys named. Only the two open maps above escape that.

## A match that ERRORS refuses — fail closed

A `match` that **compiles** but cannot be **evaluated** against a given event — a
field arriving at the wrong type, so `42 startsWith "x"` has no truth value — is
not treated as "did not match". The engine cannot *answer* whether the rule
applies, and inventing an answer in either direction is the engine deciding
enforcement on its own account. So it **refuses the action** and says why:

    the file-guard "…" could not be evaluated: its match "…" could not be
    evaluated against this PreFileUpdate (…); refusing because a guard that
    could not decide must not be read as approval

This is fail-closed, and it is recent — an earlier version skipped the binding and
let the write through, which is exactly the silent no-op this engine exists to
prevent. The practical consequence for you: **a match must be answerable**. A
field the kind declares but the event omits is filled with its type's zero value
(so `newContent == ""` still works on an empty file); a field carried at the wrong
type errors and refuses. You do not write anything special for this — but know
that a broken match blocks rather than waves things through.

## Narrowing is the match's job, not the check's

A check that re-verifies whether the event concerns it re-implements its own
match, and the two will drift. Put the narrowing in `match` and let the check
assume it applies.
