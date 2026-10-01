---
enforced: true
---

# A script's refusal condition must be a fact, not a keyword standing in for meaning

**Mistake:** a `script` check whose refusal condition is a literal-string or
keyword grep against free-form content (`event.newContent`, `changeset.files[].newContent`, a transcript
quote, a prompt) where the actual question is about MEANING — "does this
text reveal X", "is this content clean", "does this logic genuinely depend
on Y" — not a structural fact a machine can read off directly.

A grep against natural language for meaning is trivially defeated by
rewording. It loads, it validates, it sits in the project looking enforced,
and it admits any paraphrase — the exact failure this guard exists to catch,
now inside a `.sloprail/{file-guard,gate,context}/*/*.sh` this guard is
supposed to be judging, not just the guardrails a project writes.

**Not every grep is this mistake.** A grep for a literal, known, finite
name — a specific gate's own quoted refusal text, a specific env var's exact
spelling, a specific tool name — is a structural fact, and several of this
guard's own checks are exactly that kind of grep. The violation is grepping
for **meaning** via keyword proxy, not grepping at all.

**Instead:** hand the question to a `judge`. A script may still run FIRST as
a narrow, structurally-decidable pre-filter (a required env var is set, a
sibling file exists) — but the part that reasons about what content MEANS
belongs to the judge, not a keyword list standing in for it.

**Flag** a script whose refusal condition is `grep`/`contains`/string
equality applied to free-form prose or code content, deciding a question
about meaning, intent, cleanliness, or paraphrase rather than a literal,
finite, structural fact. Do NOT flag a grep for an exact, known string
(a specific gate's quoted name, a specific variable's spelling) — that is
a fact, not a heuristic.
