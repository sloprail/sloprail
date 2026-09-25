---
name: update-credential-inventory
description: Use before writing or editing any file that reads a secret out of an environment variable (an API key, a token, a signing secret) — what the security review needs recorded and where.
---

# Update Credential Inventory

Security review tracks every place this codebase pulls a secret out of the
environment in `SECURITY-INVENTORY.md`, at the repo root. A file that reads
one and isn't listed there is a gap the next review finds the hard way.

Before landing a file that calls `os.getenv` (or reads an env var by any
other means) for something secret, add or update its row:

```markdown
| File | Env var | Rotation owner | Notes |
|---|---|---|---|
| pydantic_ai_slim/pydantic_ai/providers/deepseek.py | DEEPSEEK_API_KEY | platform-team | outbound to api.deepseek.com |
```

One row per env var, not per file — a file reading two secrets gets two
rows. `Rotation owner` is whichever team's on-call would actually get paged
if the key leaked; when the file's ownership isn't obvious, `platform-team`
is the default until reassigned.
