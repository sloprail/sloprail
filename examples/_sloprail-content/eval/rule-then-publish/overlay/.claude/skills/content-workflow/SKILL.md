---
name: content-workflow
description: Use when adding a writing rule or changing a content unit's status in this repo — where rules live, and how a unit is published.
---

# Content in this repo

A **unit** is `memories/topics/<topic>/units/<NN_name>/`: `UNIT.md` holds its
frontmatter (`status: raw | drafting | published | parked`, `tags`), and
`02_draft.md` its text.

## Writing rules

A rule is `.sloprail/content-rules/<NN_name>/RULE.md`. It records what the user
asked for, so the write cites their exact words, with `sr-file` on its own in
the command:

```bash
sr-file write .sloprail/content-rules/<NN_name>/RULE.md \
  --cite:user '<exact words from the user message>' <<'EOF'
---
level: must_not          # must | must_not
applies_to: [x]          # tags it applies to; omit for every unit
---
Rule: <the rule>

PASS: <an example that follows it>
FAIL: <an example that breaks it>
EOF
```

## Publishing

Only the user publishes. Once they approve, move the unit to `published`,
record where it went out, and cite their approval:

```bash
sr-file edit memories/topics/<topic>/units/<NN_name>/UNIT.md \
  --old-string 'status: drafting' \
  --new-string 'status: published
published_urls: ["<url>"]' \
  --cite:user '<their exact words approving it>'
```

A quote must match exactly one user message; check one with
`sr-session trajectory cite '<quote>'`.
