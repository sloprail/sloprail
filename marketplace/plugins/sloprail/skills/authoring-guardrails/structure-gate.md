# The structure gate

The structure gate is an allowlist of the paths that may be written; every other write is
refused before it lands. Where a per-file rule asks "is this file right?", the structure gate
asks "may anything be written here at all?". It covers creates and updates, whether from a
Write or Edit tool or a shell command; it never refuses a delete.

Denying by default has a useful side effect: an agent that finds no allowed home for what it
is writing has to settle where the thing belongs first, rather than leave it anywhere.

It is one file per root, not a folder per rule:

```
.sloprail/file-guard/structure.yaml              # the project's: the whole tree
<plugin>/.sloprail/file-guard/structure.yaml     # a plugin's: only the folders it owns
```

## The project's structure

```yaml
# .sloprail/file-guard/structure.yaml
allow:
  - glob: ".sloprail/**"
  - glob: "memories/**/*.md"
  - regex: '^tasks/[0-9]{4}-[0-9]{2}/[^/]+\.md$'
deny:
  - regex: '\.tmp$'
```

A write lands only when it matches an `allow` entry and no `deny` entry. Each entry is exactly
one of `glob` or `regex`; use a regex for what a glob cannot say, such as a dated folder.
`deny` carves exceptions out of `allow` and is optional.

Allow `.sloprail/**`, or the next edit to any rule, this file included, is refused. A
repository with `.sloprail/` folders below its root allows `**/.sloprail/**` instead. The
project's structure has no `scope`; it covers the whole tree.

## A plugin's structure

A plugin that keeps files of its own, such as decision records under `.adr/`, ships a structure
for just those folders and declares them as its `scope`:

```yaml
# <plugin>/.sloprail/file-guard/structure.yaml
scope:
  - glob: ".mdmap/"          # a folder this plugin owns
  - glob: "**/.adr/"         # every folder named .adr, at any depth
allow:
  - glob: ".mdmap/mindmap/*/mindmap.yaml"
  - glob: "**/.adr/*.md"
```

Each `scope` entry is a glob naming a folder, ending in `/`. A plugin's `allow` and `deny`
decide only paths inside its scope; it has no say over the rest of the project.

## How a write is decided

For each path written:

1. If more than one plugin's scope holds it, the write is refused as a conflict, naming every
   owner.
2. If the project's `deny` matches it, it is refused, even inside a plugin's scope.
3. If one plugin owns it, that plugin decides. The project's `allow` does not widen a plugin's
   scope.
4. Otherwise the project's structure decides. With no project structure, the write is allowed.

Every refusal names which structure decided.

### Worked example

The mdmap plugin owns `.mdmap/` and allows `.mdmap/mindmap/*/mindmap.yaml`, denying
`.mdmap/**/*.tmp`. The project allows `docs/**` and `.mdmap/notes/*.md`, and denies
`**/*.secret`.

| Write | Decided by | Result |
|---|---|---|
| `.mdmap/mindmap/a/mindmap.yaml` | mdmap | allowed |
| `.mdmap/notes/n.md` | mdmap | refused: the project's allow does not reach into mdmap's scope |
| `.mdmap/mindmap/a/cache.tmp` | mdmap | refused by mdmap's deny |
| `.mdmap/keys/k.secret` | the project | refused by the project's deny |
| `docs/guide.md` | the project | allowed |
| `src/main.go` | the project | refused: nothing allows it |

## Mistakes that keep it from loading

A structure that does not load is reported at the next hook and enforces nothing until it is
fixed. The usual causes:

- `allow` is missing or empty;
- an entry has both `glob` and `regex`, or neither, or does not compile;
- a plugin's structure has no `scope`, or the project's has one;
- a `scope` entry is a regex, does not end in `/`, or covers the whole tree (`**/`);
- with literal scope folders, an `allow` or `deny` entry lies outside all of them.

Check what a plugin ships before installing it with
`sr-file declarations --plugin <name> path/to/plugins/<name>`.

Two plugins whose scopes overlap are reported when the session starts; every write in the
overlap is refused until one of them is switched off.

## Its test cases

The structure gate's `sr-test` cases live beside it, in
`.sloprail/file-guard/structure.tests/<case>/test.sh`.

## Turning one off

```yaml
# .sloprail/config.yaml
disabled:
  - mdmap/structure     # the mdmap plugin's structure
  - structure           # the project's own
```

A plugin's switched-off scope belongs to the project's structure again.
