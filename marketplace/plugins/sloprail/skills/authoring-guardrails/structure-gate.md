# Configuring the structure gate

The structure gate is an **allowlist**: a list of paths where writing is
allowed, with everything it covers denied by default. It is not a per-rule
check like the three natures — it is a standing boundary configured directly,
at most one `structure.yaml` per `.sloprail` root.

```
.sloprail/file-guard/structure.yaml
```

```yaml
# Only these places may be written; everything else is refused.
allow:
  - glob: "memories/**/*.md"
  - glob: "tasks/**"
deny:
  - regex: '\.tmp$'
```

Three keys. `scope` says which paths this file OWNS (see below — optional for
a project's own, required for a plugin's). `allow` is the allowlist over the
paths within scope — a write lands only if it matches one of its entries.
`deny` carves exceptions back out of `allow` (optional). Each entry, in any of
the three lists, is **exactly one of** `glob` or `regex`: a glob for the
common path shape, a regex for what a glob cannot express (a dated-folder
shape, say).

## `scope`: each file owns a slice of the tree

A project ships at most one structure.yaml, but every enabled PLUGIN may ship
its own too — and unlike a file-guard or a gate, a plugin's structure gate is
not something the project can simply out-rank. Two files with opinions about
one tree need a rule for how they combine, and that rule is `scope`.

`scope` is a list of glob/regex entries, the same shape as `allow`/`deny`,
naming the paths this file has an opinion about at all:

```yaml
# A plugin's own structure.yaml — it owns memories/tasks/ and nothing else.
scope:
  - glob: "memories/tasks/**"
allow:
  - regex: '^memories/tasks/[a-z0-9-]+/[a-z0-9-]+/[^/].*$'
```

- **A project's own** `structure.yaml` may **omit** `scope` — that means "the
  whole tree", the original meaning, and is how a project keeps one
  project-wide allowlist without naming every folder as a scope entry.
- **A plugin's** `structure.yaml` **must** declare `scope`. Without one, the
  plugin would be shipping a tree-wide, deny-by-default allowlist that locks
  the ENTIRE consuming project's tree the moment the plugin is installed — an
  unscoped plugin structure gate is refused at load (reported invalid, and
  enforces nothing) rather than let that happen silently.

## How several files compose

For a write to path `P`, every structure.yaml in force — the project's own and
each enabled plugin's — is asked whether it COVERS `P`: an unscoped file covers
everything, a scoped one covers `P` only if a `scope` entry matches it.

- **No file covers `P`** → the structure gate has no opinion, and the write is
  permitted. Composition never makes a path MORE restricted than the set of
  files that actually speak about it.
- **At least one file covers `P`** → `P` is allowed iff **at least one
  covering file's `allow` matches** and **no covering file's `deny` does** —
  the union of every covering file's `allow`, minus the union of every
  covering file's `deny`.

The practical effect: **a project's own allowlist does not need to list a
plugin-owned shape.** If `sloprail-tasks` ships the `structure.yaml` above,
a project installing it can write under `memories/tasks/eng/fix-bug/notes.md`
without ever mentioning `memories/tasks/` in its own `structure.yaml` — the
plugin's own `allow` covers it. The project's file still governs everything
IT covers (its own `scope`, or the whole tree if unscoped) exactly as before;
the plugin's piece only ever ADDS a say over the paths it scoped itself to.

A refusal names every covering file, by its qualified key and path, so an
author knows exactly where to add an `allow` — their own structure.yaml, or
that a plugin's is the one that needs adjusting (which they cannot edit
directly; see below).

## It has no per-name folder

Unlike file-guard / gate / context, the structure gate has no
`structure-gate/<name>/` folder — one file per `.sloprail` root, loaded from
`file-guard/structure.yaml`. So a refusal from it carries no rule name, only
its origin (the project's own, or the plugin that shipped it) and, since
composition, potentially several origins at once.

## Why it exists

When every place a file lands is disallowed except a declared structure, an
agent that cannot find a valid home for what it is writing is pushed to
**establish the structure first** — to decide where a thing belongs before
dumping it somewhere. That "clarify the shape before writing" behaviour is a
consequence of denying by default, not a separate rule.

The path-based allowlist here is the counterpart of the marker-based selection a
file-guard's `match` does with `markers`: one keys on where a file is, the other
on a label the file carries.

## Turning it off

Like any shipped rule, a plugin's structure gate can be switched off from the
project's side in `.sloprail/config.yaml`. Its disable key is `<plugin>/structure`
— there is no name, only the nature. Disabling it removes just that plugin's
piece; the project's own structure gate (and any other plugin's) keeps
enforcing exactly as before.
