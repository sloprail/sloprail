# Structure-gate

The structure gate is an **allowlist** of paths where writing is allowed, with
everything else it covers denied — deny-by-default. It is not a per-rule check
like the three natures; it is a standing boundary, declared in one file per
root:

```
.sloprail/file-guard/structure.yaml                   # the project's: the whole tree
<plugin>/.sloprail/file-guard/structure.yaml          # a plugin's: only its `scope`
```

The project's structure and every enabled plugin's structure load **together**
and combine. None shadows another. What keeps them from fighting is
**ownership**: the project's covers the whole tree, and a plugin's covers only
the folders it declares it owns.

It gates file **writes** — a create or an update, whether from a `Write`/`Edit`
or a Bash command that writes a file. A delete is not a write, and the
structure gate never gates one.

## The project's structure

```yaml
# .sloprail/file-guard/structure.yaml — only these places may be written.
allow:
  - glob: ".sloprail/**"
  - glob: "memories/**/*.md"
  - glob: "tasks/**"
deny:
  - regex: '\.tmp$'
```

Allow `.sloprail/**` unless the rules are deliberately frozen. A repository that keeps
`.sloprail/` folders below the root (`marketplace/plugins/<p>/.sloprail/`) allows `**/.sloprail/**`
instead, which also covers the root one. The gate covers
`.sloprail/` like any other path, so a structure that leaves it out refuses the
next edit to itself and to every rule beside it.

Two keys. `allow` is the allowlist — a write lands only if it matches one of its
entries. `deny` carves exceptions back out of `allow` (optional). Each entry is
**exactly one of** `glob` or `regex`: a glob for the common path shape, a regex
for what a glob cannot express (a dated-folder shape, say).

A write is permitted only when it matches an `allow` entry **and** no `deny`
entry. Anything else is refused before it lands.

The project's structure must **not** declare a `scope` — it implicitly covers
the whole tree, and a `scope` there is refused at load.

## A plugin's structure: `scope`

A plugin that keeps files of its own (a mind map under `.mdmap/`, decision
records under `.adr/`) ships a structure for just that part of the tree. It
**must** declare `scope`: the folders it owns.

```yaml
# <plugin>/.sloprail/file-guard/structure.yaml
scope:
  - glob: ".mdmap/"          # a folder the plugin owns
  - glob: "**/.adr/"         # every folder named .adr, at any depth
allow:
  - glob: ".mdmap/mindmap/*/mindmap.yaml"
  - glob: "**/.adr/*.md"
deny:
  - glob: ".mdmap/**/*.tmp"
```

`scope` is a list of entries shaped like `allow`/`deny`, but **glob-only**, and
each glob names a **folder** — it ends with `/`. A path is inside the scope when
one of the folders it sits in matches: `**/.adr/` owns `a/b/.adr/x.md` but not
`a/.adrx/y.md`, and `.mdmap/` owns `.mdmap/x.md` but not a file named `.mdmap`.

A plugin's `allow`/`deny` only ever decide paths inside its scope. Outside it, a
plugin's structure has no say at all — a plugin can lock down its own folders,
never the rest of the project.

## How a write is decided

For each path being written, in this order:

1. **Owners.** The plugins whose scope the path lies in. If **more than one**
   owns it, the write is **refused** as an ownership conflict, naming every
   owner — no single rule can decide it, so none is guessed.
2. **The project's veto.** If the project's structure has a `deny` entry
   matching the path, the write is **refused** — even inside a plugin's scope.
   The project always keeps the last word on its own tree.
3. **One owner.** The owning plugin decides: permitted only if its `allow`
   matches and its `deny` does not. The project's `allow` does **not** widen a
   plugin's scope — a path the project allows but the plugin does not is refused.
4. **No owner.** If the project has a structure, it decides (its `allow` must
   match, its `deny` must not). If it has none, the write is **permitted**.

Every refusal names the source that decided: the project's structure gate, or
plugin X's structure gate with the scope that matched and the file it lives in,
or every plugin in an ownership conflict.

## What is refused at load

A structure.yaml that breaks one of these is reported (at session start, and by
`sr-file declarations`) and **not loaded** — it enforces nothing until fixed:

- `allow` is missing or empty;
- an `allow`/`deny` entry sets both or neither of `glob`/`regex`, or does not
  compile;
- a **plugin's** structure has no `scope`, or an empty one;
- the **project's** structure has a `scope`;
- a scope entry is a `regex` (glob-only for now), does not end in `/`, starts
  with `/` or `./`, or covers the whole tree (`/`, `*/`, `**/`, `**`, the empty
  glob, or anything made only of wildcards and slashes);
- when every scope entry is a **literal** folder (no `*`, `?`, `[`), an
  `allow`/`deny` entry lies outside all of them — a glob must start with one of
  the scope folders, a regex must be anchored with `^` and start with one. With a
  wildcard scope this cannot be checked before a path arrives, so it is skipped
  (an entry outside the scope simply never applies).

To check what a plugin ships before installing it, load it as a plugin:

```
sr-file declarations --plugin mdmap path/to/plugins/mdmap
```

## Conflicts between plugins

Two plugins whose **literal** scopes overlap — the same folder, or one inside the
other (`.mdmap/` and `.mdmap/mindmap/`) — are reported at session start, naming
both. Both stay loaded, and every write inside the overlap is refused as an
ownership conflict until one is disabled. Overlaps involving a wildcard scope
cannot be seen at load; they surface as the same refusal when a write lands in
them.

Session start also lists which part of the tree each plugin's structure owns,
so a person learns up front that writes under `.mdmap/` answer to that plugin.

## Turning one off

From the project's `.sloprail/config.yaml`:

```yaml
disabled:
  - mdmap/structure     # drop plugin mdmap's structure entirely
  - structure           # drop the project's own
```

A disabled plugin's structure is gone completely: its former scope becomes
unowned, so the project's structure decides there — or, with none, writes there
are permitted. Disabling also silences an overlap that plugin caused, and a
plugin structure that will not load (the consumer cannot fix a file inside an
install cache) is switched off the same way.

## Worked example: mdmap owns `.mdmap/`

The mdmap plugin ships:

```yaml
scope:
  - glob: ".mdmap/"
allow:
  - glob: ".mdmap/mindmap/*/mindmap.yaml"
deny:
  - glob: ".mdmap/**/*.tmp"
```

The project ships:

```yaml
allow:
  - glob: "docs/**"
  - glob: ".mdmap/notes/*.md"
deny:
  - glob: "**/*.secret"
```

| Write | Owner | Result |
|---|---|---|
| `.mdmap/mindmap/a/mindmap.yaml` | mdmap | permitted — mdmap allows it (the project need not) |
| `.mdmap/notes/n.md` | mdmap | refused by mdmap — the project's `allow` does not widen mdmap's scope |
| `.mdmap/mindmap/a/cache.tmp` | mdmap | refused by mdmap's `deny` |
| `.mdmap/keys/k.secret` | mdmap | refused by the project's `deny` (the veto) |
| `docs/guide.md` | — | permitted by the project |
| `src/main.go` | — | refused by the project (deny-by-default) |

With `disabled: [mdmap/structure]`, `.mdmap/` is unowned: `.mdmap/notes/n.md` is
then permitted by the project and `.mdmap/mindmap/a/mindmap.yaml` refused by it.
With no project structure either, every write under `.mdmap/` is permitted.

## Why it exists

When every place is disallowed except a declared structure, an agent that cannot
find a valid home for what it is writing is pushed to **establish the structure
first** — to decide where a thing belongs before dumping it somewhere. That
"clarify the shape before writing" behaviour is a consequence of denying by
default, not a separate rule. Scopes let a plugin bring that discipline to the
folders it owns without taking the rest of the project with it.

The path-based allowlist here is the counterpart of the marker-based selection a
file-guard's `match` does with `markers`: one keys on where a file is, the other
on a label the file carries.
