# Matchers

An expression over the event's **own fields** — the ones the load check names
for that kind, and nothing else. No filesystem, no environment, no other events.
It must evaluate to a boolean. Absent means every occurrence.

A **misspelled top-level field is caught at load**: the binding is refused by
name and session start prints what the kind does carry. You need not defend
against that one.

## Operators on a field of type `string`

| | |
|---|---|
| `startsWith` | `path startsWith "memories/"` |
| `endsWith` | `path endsWith ".md"` |
| `contains` | `path contains "/decisions/"` |
| `matches` | `path matches "^docs/[0-9]+-"` (regular expression) |
| `&&` `\|\|` `!` | `path startsWith "src/" && !(path endsWith "_test.go")` |
| `==` `!=` | `path == "README.md"` |

## A field of type `list`

Read with `any`, `all`, `none` and `len`:

```
any(invocations, .bin == "curl")
!any(invocations, .bin == "npm")
len(invocations) > 1
```

Check the type printed beside each field — the two groups are not
interchangeable.

A field of type `bool` is used directly: `resultKnown && newContent contains "---"`.

**The inside of a list is checked only where its element is declared.** A module
that declares the element's shape gets a mistyped key refused at load, with the
real keys named. Where the element is left open — a map whose keys belong to the
program being run rather than to the engine — a key read off an element is
verified against nothing: a mistyped one compiles, loads, and evaluates to false
forever. The load check tells you which you have. Confirm a list matcher by
causing the event.

## There is no glob

Not an omission. A glob's semantics differ between tools enough that a
familiar-looking pattern would be familiar and subtly wrong — whether `*`
crosses a directory separator is not the same answer in two places. Write what
you mean:

| want | write |
|---|---|
| everything under `memories/` | `path startsWith "memories/"` |
| every markdown file | `path endsWith ".md"` |
| markdown under `memories/` | `path startsWith "memories/" && path endsWith ".md"` |
| a real pattern | `path matches "^memories/[0-9]{8}_"` |

Narrowing is the matcher's job, not the hook's. A hook that re-checks whether
the event concerns it re-implements its own binding, and the two will drift.
