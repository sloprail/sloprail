---
enforced: true
---

# Prefer the engine's file events to reading the trajectory

**Mistake:** reconstructing which files a turn touched by parsing the transcript
— a tool-name allowlist plus a regex for paths.

```sh
# WRONG
$tn | test("^(Write|Edit|MultiEdit|NotebookEdit)$")   # a name allowlist
$args | test("memories/updates/")                     # a regex over stringified args
```

**Why it fails, both measured:**

- The allowlist goes stale silently. Claude Code renamed `Task` to `Agent` in
  v2.1.63 and every rule matching on names stopped seeing those turns without
  erroring. `NotebookEdit` carries `notebook_path`, not `file_path`, and was
  missed by every such rule for the same reason.
- The regex matches a path *mentioned* anywhere in the arguments — inside an
  `echo`, a `grep`, a comment — and misses everything the shell parser resolves:
  `sh -c 'rm x.md'`, wrappers, redirections, `$VAR`.

**Instead:** bind the file kinds and let the engine tell you. It parses the
command line, unwraps interpreters and wrappers, and reports paths it resolved.
A rule about the turn as a whole records what the per-file rules saw into
`sr-session state` and reads it back at the cycle's end.

**The trajectory is still right for one thing:** what the agent *said*. A
hashtag, a claim, a skill it loaded — there is no event for saying a thing.

## How the check detects it

Any two tool names joined by `|` in one alternation. One name is a mention; two
in an alternation is a dispatch on tool identity.

Worth recording: the first pattern written for this required a `|` or `)` after
a name, and the real case — `test("^(Write|Edit|MultiEdit|NotebookEdit)$")` —
ends with `$)` and slipped through. The rule was measured against the actual slop
that prompted it, and only then did it catch it.
