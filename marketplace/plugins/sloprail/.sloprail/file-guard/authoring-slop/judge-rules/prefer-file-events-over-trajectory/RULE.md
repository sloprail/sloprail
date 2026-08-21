---
enforced: true
---

# Prefer the engine's file events to reading the trajectory

**Mistake:** reconstructing which files a turn touched by parsing the transcript —
a tool-name allowlist (`Write|Edit|MultiEdit|NotebookEdit`) plus a regex over the
stringified tool arguments to pull out paths.

**Why it fails, both measured:**

- The allowlist goes stale silently. Claude Code renamed `Task` to `Agent` and
  every rule matching on names stopped seeing those turns without erroring.
  `NotebookEdit` carries `notebook_path`, not `file_path`, and was missed the same
  way.
- The path regex matches a path *mentioned* anywhere in the arguments — inside an
  `echo`, a `grep`, a comment — and misses everything the shell parser resolves:
  `sh -c 'rm x.md'`, wrappers, redirections, `$VAR`.

**Instead:** bind the file event kinds and let the engine tell you. It parses the
command line, unwraps interpreters and wrappers, and reports the paths it
resolved. A rule about the turn as a whole records what the per-file checks saw
into `sr-session state` and reads it back at the cycle's end.

The trajectory is still right for one thing — what the agent *said*: a hashtag, a
claim, a skill it loaded. There is no file event for saying a thing.

**Why the judge and not only the grep.** The grep catches the narrow signature —
two tool names joined by `|` in one alternation. The judge reasons about the
*approach*: a script walking `$SR_TRANSCRIPT` to find touched files, or matching
tool names one at a time so no `|`-alternation appears, is the same mistake the
grep's signature does not cover.

**Flag** a script that reads `$SR_TRANSCRIPT` (or a `.jsonl` session file) to
reconstruct which files a turn wrote, or dispatches on tool identity to do so,
when a file event binding would answer the same question. Do NOT flag reading the
transcript for what the agent SAID (a claim, a hashtag) — that has no event.
