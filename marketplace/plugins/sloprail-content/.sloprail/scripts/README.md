# Shipped script rules

A **script rule** names one of these (or a project-local script — see the
plugin README) under `script.name` in a `RULE.md`/`CONSTRAINT.md`'s frontmatter,
never inline shell. Every script here reads the same `CheckPayload` any check
reads, on stdin, and follows the same refusal contract: exit 0 permits, a
non-zero exit refuses with `{"reason": "..."}` on stdout.

## char-limit.sh

Character-count limit on the unit's body, optionally split into segments by a
literal delimiter.

```yaml
script:
  name: char-limit
  args: ["<limit>", "<delimiter>"]   # delimiter optional
```

- `X post/thread ≤ 280 per tweet`: `args: ["280", "---"]` where the draft
  separates tweets with a line containing exactly `---` (document the delimiter
  the topic actually uses; the script does not assume one).
- `HN title ≤ 80`: `args: ["80"]` (no delimiter — a title is one segment).
- `Reddit title ≤ 300`: `args: ["300"]`.

## banned-phrases.sh

Refuses if the body matches any pattern in a project-supplied list — literal
substrings or `/regex/` patterns (PCRE, case-insensitive), one per line, `#`
comments and blank lines skipped.

```yaml
script:
  name: banned-phrases
  args: ["<project-relative-path-to-list>"]
```

The list is **not** shipped by this plugin — the project owns its own AI-tells
list (specific words, em-dash overuse via a regex like `/—{2,}/`, whatever house
style bans). A typical location: `.sloprail/content-rules/banned-phrases.txt`.

## Adding a project-local script

A script rule's `script.name` is resolved first against this plugin's
`scripts/`, then against the consumer project's own
`.sloprail/content-rules/scripts/<name>.sh` — for a deterministic check specific
to one project (a house character-count quirk, a required disclosure line) that
does not belong in a shared plugin. Same contract: stdin CheckPayload, exit-code
verdict, `{"reason": ...}` on refusal.
