# Invariants

What sloprail promises, whatever harness runs it. One file per invariant:
`spec/invariants/<area>/<id>.yaml`, `<id>` kebab-case and unique within its area.
An invariant is referred to as `<area>/<id>`.

```yaml
statement: >-
  One always-true sentence (or two) about behaviour a user of sloprail can
  observe: what happens, not how the code does it.
needs: [pretooluse-refusal]   # optional
```

- `statement` is harness-neutral: no Claude Code event names, payload fields,
  tool names or file paths of one harness. Those belong to the harness
  adapter, and to the capability it provides.
- `needs` lists the harness capabilities the invariant relies on, by their id
  in harness-mocks `spec/capabilities/` (registry read at harness-mocks
  `54784f0`). No `needs` means the invariant holds without any harness: CLI
  commands over a git repository, or logic fed a neutral event.

## Baselines

The code each area's invariants were last read from. When sloprail moves past a
baseline, `git diff <baseline>..main -- <the area's code>` is what to re-read.

| areas | sloprail | harness-mocks registry |
|---|---|---|
| all (first draft) | `1cfd34b` (2026-10-07) | `54784f0` |

## Areas

| area | covers |
|---|---|
| `loading` | finding and validating rules, precedence, `disabled:`/`enabled:`, `config.yaml` |
| `events` | event kinds and fields; extraction from file writes, commands, tool calls, tags, Stop |
| `matching` | match scopes, load-time type checks, fail-closed evaluation |
| `checks` | the check contract: `require` then `checks`, exit status is the verdict, `prepare`, `when`, script environment |
| `judges` | binary verdict, retries, fixed refusal words, templates, `sr-agent` |
| `gates` | pre-action dispatch, ordering, multi-file refusals, attribution |
| `structure` | where files may land: deny by default, plugin ownership |
| `contexts` | `enter`/`exit`, active state and payload |
| `fileguard` | commit range, `subjects`, `deletions`, renames, commit-required |
| `cache` | verdict key, what is stored and replayed, `verify`, the results store, judge slots |
| `citations` | quotes resolving, pools, commit trailers, `sr-file` |
| `session` | baseline, state, read mark, Stop block cap, tracked ranges |
| `subagents` | a sub-agent as its own session, background agents, handing a change back |
| `cli` | the `sr` proxy, exit codes, `sr-mark`, `sr-file validate`, `sr-checks` surface |
| `install` | plugin hook wrapper, binary install, refusing writes when the binary is missing |
| `authoring-tools` | `sr-test`, `sr-eval` |

Out of scope here: the shipped `examples/` (each is its own product), docs, and
this repository's own `.sloprail/` rules.

## Linking (from stage S3)

- `// sr:invariant <area>/<id>` on the code that upholds it.
- `// sr:proves <area>/<id>` on the tests that prove it.
