---
enforced: true
---

# A script's refusal condition must be a fact, not a keyword standing in for meaning

**Flag** a `script` check whose refusal condition is `grep`/`contains`/string
equality applied to free-form content (`event.newContent`,
`changeset.files[].newContent`, a transcript quote, a prompt) to decide a question
about MEANING — "does this text reveal X", "is this content clean", "does this
logic genuinely depend on Y" — rather than a structural fact a machine can read
off directly. Do NOT flag a grep for a literal, known, finite name (a specific
gate's quoted refusal text, a variable's exact spelling, a tool name): that is a
fact, and several of this guard's own checks are exactly that kind of grep.

A grep against natural language for meaning is trivially defeated by
rewording. It loads, it validates, it sits in the project looking enforced,
and it admits any paraphrase — the exact failure this guard exists to catch,
now inside a `.sloprail/{file-guard,gate,context}/*/*.sh` this guard is
supposed to be judging, not just the guardrails a project writes.

**Instead:** hand the question to a `judge`. A script may still run FIRST as
a narrow, structurally-decidable pre-filter (a required env var is set, a
sibling file exists) — but the part that reasons about what content MEANS
belongs to the judge, not a keyword list standing in for it.
