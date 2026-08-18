# Decision: fileguard config format — first pass on enforce-structure.sh

Status: DRAFT — not published. Comparing two candidate shapes before freezing anything.

## Input: the concrete prototype

`.claude/hooks/enforce-structure.sh` in a10n-strategy does three things in one
PreToolUse hook, in this order:

1. **STRUCTURE LOCKDOWN** — a whitelist of `case` patterns; any write outside
   them is denied outright. No check runs, no matcher decides "is this
   relevant" — it's a closed list, deny-by-default.
2. **SKILL-LOAD** — for two specific path prefixes
   (`docs/private/topics/*`, `docs/private/decisions/*`), a specific skill
   must have fired as a `Skill` tool_use in the transcript before the write
   is allowed.
3. **FRONTMATTER** — for two specific basenames (`TOPIC.md`, `DECISION.md`,
   `UNIT.md`), a *new* file's content must start with a YAML frontmatter
   block carrying a non-empty `transcript_path`.

## Two candidate groupings

### Option A — two formats: path-checks + whitelist

**Format 1: path-checks.** A matcher (glob or expr) paired with a list of
checks. Each check is a *kind* (`skill`, `schema`, ...) with kind-specific
fields. (2) and (3) both fit this shape — they differ only in what the check
kind is.

```yaml
# .sloprail/require-context/config.yaml
require:
  - match: "docs/private/topics/**"
    checks:
      - skill: document-topic
  - match: "docs/private/decisions/**"
    checks:
      - skill: document-strategy
  - match: "**/TOPIC.md"
    checks:
      - schema: topic-frontmatter.cue
  - match: "**/DECISION.md"
    checks:
      - schema: decision-frontmatter.cue
```

**Format 2: whitelist.** No checks, no per-entry structure beyond the glob
itself — a flat list, deny-by-default outside it.

```yaml
# .sloprail/structure-lockdown/config.yaml
allow:
  - "docs/private/decisions/**"
  - "docs/private/concepts/**"
  - "docs/private/topics/**"
  - "docs/private/branding/**"
  - "docs/private/CLAUDE.md"
  - "CLAUDE.md"
  - "README.md"
  - ".claude/**"
  - ".gitignore"
  - ".mcp.json"
```

Two plugins, two configs, two mental models: "what may exist here" (whitelist)
vs "what must be true before you write here" (path-checks).

### Option B — one format: whitelist is a path-check with an empty checks list

If a whitelist entry is just `match: X` with no `checks:`, its meaning is "the
match is required, nothing further is." Under this reading, (1)/(2)/(3) are
the SAME format, and "whitelist" degenerates to path-checks with the checks
array empty (or omitted):

```yaml
# .sloprail/fileguard/config.yaml
rules:
  # (1) whitelist entries — no checks, presence of the match IS the rule.
  # But then "everything NOT matched" needs its own meaning — see the open
  # question below; the prototype's whitelist is deny-by-default globally,
  # not "each entry independently permits."
  - match: "docs/private/decisions/**"
  - match: "docs/private/concepts/**"
  - match: "docs/private/topics/**"
    checks:
      - skill: document-topic
  - match: "**/TOPIC.md"
    checks:
      - schema: topic-frontmatter.cue
  - match: "docs/private/decisions/**"
    checks:
      - skill: document-strategy
  - match: "**/DECISION.md"
    checks:
      - schema: decision-frontmatter.cue
```

## Where option B breaks — the reason to prefer A

The whitelist's actual semantics in the prototype is **deny-by-default over
the WHOLE tree, evaluated once, before anything else runs** — a write outside
every listed pattern is refused regardless of any other rule. Path-checks'
semantics is **opt-in, evaluated per-entry** — a write that matches nothing is
just... not checked, which is silence, not a refusal.

These are opposite defaults. Folding them into one format means every
consumer of "no match found" has to ask which of the two behaviors was meant,
and the format itself carries no signal for that — a config author has to
know, out of band, "this plugin happens to deny-by-default; that one doesn't."
That is exactly the kind of implicit-knowledge cost the whole induction
exercise is trying to surface *before* it gets baked into 17 configs.

**So: two separate, small formats — not because they look different, but
because their absence of a match means the opposite thing.** A merge that
papers over that is the premature-generalization trap in miniature.

## What's still open, deliberately not decided here

- Whether `checks:` entries beyond `skill`/`schema` (script? prompt/rubric?)
  belong in the SAME check-kind vocabulary, or whether path-checks itself
  needs to fork once a third check kind shows up with a shape that doesn't
  fit "one field naming what to check." Not answered — no third check kind
  has been tested against this yet.
- Whether `checks:` items run at PreToolUse (skill: can, per the unit — deny
  before the write lands) or need a Post/Stop phase (schema: on new-file
  content, which IS available pre-write here, but a schema check on a
  DERIVED file wouldn't be). Not answered — this prototype's schema check
  happens to run pre-write on content already in hand.
- Whitelist's relationship to path-checks at the ENGINE level — does a
  project run one plugin of each, or does whitelist become a flag on
  path-checks ("and also: nothing outside these globs is writable at all")?
  Not decided; kept as two independently-installable plugins for now because
  that is what makes each easy to test in isolation per the induction plan.

## Next step

Confirm which grouping (A vs B) to freeze for path-checks + whitelist, before
touching use-case #12 or #4.
