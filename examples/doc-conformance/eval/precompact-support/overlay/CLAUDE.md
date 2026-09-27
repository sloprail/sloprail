# harness-mocks

`claude-mock/` emulates real Claude Code for tests: its hook events, their
payload fields, its CLI flags and its session files.

## Doc citations

Code that emulates a real Claude Code behavior cites the doc page it follows
in a comment on its own line, next to that code:

```go
// sr:docs https://code.claude.com/docs/en/hooks#sessionstart
```

The `a10n:docs` comments across the tree are the older spelling of the same
citation. New or changed code uses `sr:docs`; the `mock-conformance` skill
has the details.
