# Writing a match

A `match` narrows a rule to what it is about: the files a file-guard judges, the events a
gate or a context wakes on. It is an expression that must come out true or false. Without
one, a gate or context trigger applies to every event of its kind; a file-guard must have
one.

Put all the narrowing in `match`. A check that re-tests whether the event concerns it
duplicates the match, and the two drift apart.

## What a match can read

A file-guard and a gate read the same fact under different names. This is the mistake to
avoid: `path` bare in a file-guard, `event.path` in a gate or a context.

### In a file-guard: the file's own facts

| name | type | |
|---|---|---|
| `path` | string | the file's path, relative to the repository root |
| `status` | string | `A`, `M`, `D` or `R`: the file's net change across the commits |
| `markers` | list | the `sr:` markers the file carries after the change, each `{kind, fqn, line}` |
| `oldMarkers` | list | the markers it carried before |
| `trailers` | map | each commit-message trailer in the range, to its list of values |

A renamed file is selected when the match holds on its new path or its old one. A file-guard
judges commits with no session, so it cannot read a context.

```
path endsWith "SKILL.md"
any(markers, .kind == "invariant") or any(oldMarkers, .kind == "invariant")
status == "A" and "move-only" in (trailers["Sloprail-Refactor"] ?? [])
```

Read `oldMarkers` too when the rule guards a marker: the change that removes a file's last
marker leaves `markers` empty, and `any(markers, .kind == "invariant")` alone would never
select it.

### In a gate or a context: the event, under `event`

| name | type | |
|---|---|---|
| `event` | structure | the event's fields, as its kind declares them ([events.md](events.md)) |
| `context` | map | every declared context by name, each `{active, payload}` |

```
event.path startsWith "memories/decisions/"
any(event.invocations, .bin == "curl")
not context["tag-declared"].active
```

A field the trigger's kind does not carry (`event.paht`, or `event.path` on a
`PreCommandInvoke` trigger) fails to load.

## Operators

| | |
|---|---|
| `startsWith`, `endsWith`, `contains` | `path startsWith "memories/"` |
| `matches`, a regular expression | `path matches "^docs/[0-9]+-"` |
| `==`, `!=` | `path == "README.md"` |
| `and`, `or`, `not` | `path startsWith "src/" and not (path endsWith "_test.go")` |
| `in` | `"next" in .flags.tag` |

Read a list with `any`, `all`, `none` and `len`:

```
not any(event.invocations, .bin == "npm")
len(event.invocations) > 1
any(event.invocations, .bin == "rm" and any(.argv, # == "-rf"))
```

A `bool` field is used as it is: `event.resultKnown and not (event.newContent contains "---")`.

## A glob, in a file-guard

A file-guard may give a bare glob instead of an expression:

```yaml
match: "**/*.md"
```

A value with no space and no quote is a glob; anything else is an expression. Quote it in YAML
when it starts with `*`. `**` crosses folders, `*` matches within one, `?` is one character,
and `[...]` is a character class. Braces are literal, so write alternatives as an expression
with `or`. Gates and contexts have no glob form.

## Reads the loader cannot check

The loader refuses a misspelled field, including one inside a list predicate
(`any(event.invocations, .bni == …)`). Two reads it cannot check, so a typo there is
false forever and the rule never fires. Cause the event and watch the rule fire before
trusting either:

- **A context name.** Contexts are named by the project, so `context["reserch-run"].active`,
  misspelled, reads as absent rather than failing.
- **A command's flag.** Flag names belong to the command, so `.flags.tagg` reads as an empty
  list. A flag's values are always a list: write `"next" in .flags.tag`, never
  `.flags.tag == "next"`, which fails to load.

## A match that cannot be evaluated refuses

A match that loads but cannot be evaluated for a given event, such as a field arriving with
the wrong type, does not count as "did not match". The rule cannot tell whether it applies,
so the action is refused with a message saying so. A broken match blocks; it never waves
things through.
