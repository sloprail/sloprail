# The structure gate

The structure gate is a single, tree-wide **allowlist**: a list of paths where
writing is allowed, with everything outside denied. Deny-by-default over the
whole project. It is not a per-rule check like the three natures — it is one
standing boundary a project configures directly.

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

Two keys. `allow` is the allowlist — a write lands only if it matches one of its
entries. `deny` carves exceptions back out of `allow` (optional). Each entry is
**exactly one of** `glob` or `regex`: a glob for the common path shape, a regex
for what a glob cannot express (a dated-folder shape, say).

A write is permitted only when it matches an `allow` entry **and** no `deny`
entry. Anything else is refused before it lands.

## It is a singleton, not a per-name rule

Unlike file-guard / gate / context, the structure gate has no
`structure-gate/<name>/` folder — there is one per project, loaded from the
`file-guard/structure.yaml` file. So a refusal from it carries no rule name, only
its origin (the project's own, or the plugin that shipped it).

## Why it exists

When every place is disallowed except a declared structure, an agent that cannot
find a valid home for what it is writing is pushed to **establish the structure
first** — to decide where a thing belongs before dumping it somewhere. That
"clarify the shape before writing" behaviour is a consequence of denying by
default, not a separate rule.

The path-based allowlist here is the counterpart of the marker-based selection a
file-guard's `match` does with `markers`: one keys on where a file is, the other
on a label the file carries.

## Turning it off

Like any shipped rule, a plugin's structure gate can be switched off from the
project's side in `.sloprail/config.yaml`. Its disable key is `<plugin>/structure`
— there is no name, only the nature, because the gate is a singleton.
