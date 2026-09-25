# sloprail-content

Guardrails for a `memories/topics/<topic>/units/` content workflow — a unit's
own frontmatter **taxonomy** decides which writing rules apply, a deterministic
**script** catches what an LLM is bad at (character limits, banned phrases), a
**judge** weighs the judgement-call rules (style, tone, "does this sound
AI-written"), and a unit cannot reach `status: published` without a **cited
user approval**. Two guardrails, in the nature format — file-guards under
`.sloprail/`, with `match:` / `checks:`, flat `.event` fields, and refusals
delivered as a non-zero exit carrying `{"reason": …}` on stdout.

## The taxonomy

A unit's frontmatter (`.sloprail/schemas/unit.cue`) carries three selector
axes, backward compatible with the pre-existing `UNIT.md` shape — every field
below is optional, and a unit with none of them still validates and still gets
every rule whose selector is empty:

| axis | example | what it's for |
|---|---|---|
| `channels` | `[x]`, `[reddit]`, `[hn, github-readme]` | WHERE it is going out. An open string list — a project adds a channel by using it, no schema edit. |
| `type` | `post \| thread \| wedge \| video` | The pre-existing content-shape field, reused as a taxonomy axis. |
| `tags` | `[technical-deep-dive]` | Free topical tags for anything channel/type doesn't capture. |

Example unit frontmatter:

```yaml
---
transcript_path: /Users/nikita/.claude/projects/.../abc.jsonl
created: 2026-09-25
type: thread
status: drafting
channels: [x]
tags: [launch]
---
```

A rule declares `applies_to:` over these same three axes; **no selector at
all** means global (every unit). Each axis present is OR'd within itself
(a unit matches if ANY of the rule's listed values is present) and AND'd
across axes:

```yaml
applies_to:
  channels: [x, reddit]   # channels include x OR reddit
  type: [post]            # AND type is post
```

So `applies_to: {channels: [x]}` is "the X rules", and no `applies_to` at all
is "the global style rules" — exactly the framing the taxonomy was asked for.

## The rule format, and where rules live

**On file naming:** a sibling in-flight change to `sloprail-tasks` moves a
task's start conditions to per-condition files (`gates/<name>.md` for a judge,
`gates/<name>.sh` for a script). This plugin keeps `RULE.md`/`CONSTRAINT.md` —
ONE file per rule holding BOTH the frontmatter (`level`, `applies_to`,
`transcript_paths`, and, for a script rule, `script: {name, args}`) and the
body — rather than splitting a rule across a `.md` and a `.sh`. The reason:
a content rule's mechanism (judge vs. script) is a property of ONE rule
object with one origin and one scope, not two independently-triggerable
conditions the way a task's several start gates are; splitting it would mean
a script rule's `.sh` carries no `applies_to`/`level`/citations of its own
(those would have to live in a paired `.md` anyway, since a shipped script
like `char-limit.sh` is reused by many rules with different args), so the
split buys no real separation here and would just mean two files to keep in
sync for every rule. The frontmatter+body-in-one-file shape is also what
`unit-satisfies-constraints`'s existing `CONSTRAINT.md` already uses, so
keeping it is also the zero-friction migration path (see below).

A rule is a `RULE.md` (project-wide) or `CONSTRAINT.md` (topic-scoped, see
Migration below), frontmatter validated against `.sloprail/schemas/rule.cue`:

```yaml
---
level: must_not
created: 2026-09-25
transcript_paths: ["/abs/session.jsonl:88"]
applies_to:
  channels: [x]
---
Rule: no rhetorical questions as openers ("Ever wonder why...?").

PASS: "I shipped X because Y broke in prod."
FAIL: "Ever wonder why your builds keep failing?"
```

- `level`: `must` or `must_not`, both hard — there is no advisory tier.
- `transcript_paths`: provenance, where the rule came from. Not verified by any
  guardrail (unlike `approved:` below) — an audit trail, not a check.
- `applies_to`: the taxonomy selector above; absent = global.
- **Either** a judge rule (the body above is put to a model — style, tone,
  positioning, "does this read as AI slop") **or** a script rule:

```yaml
---
level: must
applies_to:
  channels: [x]
script:
  name: char-limit
  args: ["280", "---"]
---
Script rule: X post/thread character limit (280 per tweet, split on a literal
`---` line as the thread delimiter). Deterministic — see .sloprail/scripts/README.md.
```

A script rule names a script (shipped by this plugin under
`.sloprail/scripts/` — inside the `.sloprail` tree so it installs with the
rest of the plugin — or by the project under
`.sloprail/content-rules/scripts/`) plus its arguments — **never inline
shell**. See `.sloprail/scripts/README.md` for the shipped ones
(`char-limit`, `banned-phrases`).

### Where rules live in the consumer project

Two locations, both collected for every unit and merged into one applicable
set:

1. **Project-wide**, `.sloprail/content-rules/<NN_name>/RULE.md` — selected by
   `applies_to`. This is new with this plugin: a channel/type/tag rule that
   applies across every topic (the X rules, the global style rules), which the
   old topic-local guard had no way to express.
2. **Topic-scoped**, `memories/topics/<topic>/constraints/<NN_name>/CONSTRAINT.md`
   — the pre-existing shape, unchanged in location or filename. Scope is
   implicit (only units under that topic see it); an `applies_to` on a
   topic-scoped rule is an *additional* filter within the topic, and most
   topic rules omit it entirely, exactly as before.

## The guardrails

### unit-satisfies-rules — file-guard, Stop after-check

On a unit's `UNIT.md` or `02_draft.md` write: collects every rule the unit's
own taxonomy selects (project-wide + its topic's `constraints/`), runs every
**script** rule deterministically first (cheap, fails closed, no model), then
sends every **judge** rule in one call to the engine's judge (`model: size-md`
— the cheapest adequate tier for a real judgement call, not the largest
default and not the smallest). Refuses on any failing `must`/`must_not`,
naming the rule folder, its level, and why. A unit selecting no rules at all
(no global rules configured, no matching channel/type/tag rule, no topic
constraints) passes trivially — nothing to check is not a violation.

### unit-publish-approved — file-guard, **preventive**

A unit cannot reach `status: published` without **both**, in its frontmatter:

- `approved:` — a `[quote](jsonl)` citation link, the *exact* shape and
  grounding mechanism `sloprail-tasks`'s task body uses for the human's ask.
  The quote must resolve, via `sr-session trajectory cite --source-types
  user`, to a **real user message**. An agent's own prior turn, a tool
  result, or a harness-injected message (`<system-reminder>`,
  `<task-notification>`, …) does not ground — `cite` excludes all three by
  construction — so an agent cannot cite its own output as the approval that
  authorizes itself to publish.
- `published_url:` — where it actually went out.

```yaml
approved: "[go ahead, ship it](/Users/nikita/.claude/projects/.../abc.jsonl:42)"
published_url: "https://x.com/nikita/status/1234567890"
```

Publishing is the irreversible step, and the task is explicit that an agent
must not be able to publish on its own say-so — so this guard, like
`content-rule-is-grounded` below, is bound `preventive: true`. The citation
grounding itself is not reimplemented — `unit-publish-approved/cite-links.sh`
is `sloprail-tasks`'s own `cite-links.sh`, vendored verbatim (provenance noted
at its top) because two independently-installed plugins cannot share a file by
reference. Vendoring rather than reinventing keeps "does this quote ground to
a real user message" answered identically by both plugins.

### content-rule-is-grounded — file-guard, **preventive**

Protects the RULE SET itself — the same role `task-body-is-human-authored`
plays for a task's ask, one level down. On a `RULE.md` or `CONSTRAINT.md`
write, two independent, deterministic checks, both fail-closed:

1. **Grounded provenance.** At least one `transcript_paths` entry must
   resolve, via the same `cite --source-types user` mechanism, to a line that
   is a **real user message** — not the agent's own prior turn, not a tool
   result, not a harness-injected message. An agent cannot invent a writing
   rule nobody asked for ("always mention the product name") and have it
   silently start gating every future unit; it must be traceable to an actual
   ask, the same way a task's body must be traceable to the human's words.
2. **A script rule must be capable of refusing.** A structural check (grep-
   shaped, the same style `sloprail`'s own `authoring-slop` guard uses for
   guardrail authoring generally) refuses a script rule whose script contains
   no conditional non-zero exit or `refuse()`-style call anywhere — an
   unconditional `exit 0` dressed up as an enforcement. This is a floor, not a
   proof: it establishes that a real refusal SHAPE exists in the source, not
   that it is reachable for every input (see
   `content-rule-is-grounded/can-refuse.sh`'s header for what it deliberately
   does not claim). A rule that is a genuine, deliberate no-op can still be
   shipped by documenting that choice in the rule's own body, the same
   override convention `authoring-slop` uses.

## Migration from the topic-local `unit-satisfies-constraints` guard

If a project (like `strategy`) already has the old, topic-only guard:

1. Install this plugin — `unit.cue` and `rule.cue` under the project's
   `.sloprail/schemas/`.
2. **`constraints/` needs no changes.** `unit-satisfies-rules`'s
   `rules-lib.sh` collects a unit's topic constraints the exact same way the
   old guard's `prepare.sh` did (same directory, same filename, same
   `level`/PASS-FAIL shape — `rule.cue` is a superset of the old
   `CONSTRAINT.md` frontmatter, adding only the optional `applies_to`).
   **`content-rule-is-grounded` only checks a `CONSTRAINT.md` on its next
   WRITE** (create or edit) — installing this plugin does not retroactively
   walk existing constraint files, so an old `CONSTRAINT.md` whose
   `transcript_paths` does not actually ground (or is missing) keeps working
   until someone next edits it, at which point it must carry a real,
   resolving citation to pass. This is deliberate: the guard protects new
   authoring, not a one-time migration audit.
3. Add project-wide rules under `.sloprail/content-rules/` for anything that
   should apply across topics by channel/type/tag — the X rules, the global
   style rules, the deterministic limits — which is the actual generalization
   this plugin adds.
4. Remove the old `.sloprail/file-guard/unit-satisfies-constraints/` folder
   once satisfied the new guard covers the same ground (or disable it via
   `.sloprail/config.yaml`'s `disabled:` list first, to compare side by side).
5. Add `approved:`/`published_url:` to any unit workflow that sets
   `status: published`, and install `unit-publish-approved`.

No unit on disk needs to change: `channels`/`tags` are optional, and a unit
with neither still gets every global rule and its topic's constraints, same as
before this plugin existed.

## The structure gate piece

`.sloprail/file-guard/structure.yaml` is this plugin's own contribution to the
project-wide structure gate (a single, deny-by-default tree allowlist — see
the `sloprail` base plugin's own docs) — the paths this plugin's guardrails
read from or write to: `memories/topics/**` (units, their topic-scoped
constraints, TOPIC.md, distribution/) and `.sloprail/content-rules/**`
(project-wide rules, project-local script rules, banned-phrase lists). The
engine composes a plugin's own structure piece into a project's structure gate
by `scope`; until that composition is wired up, a project enforcing its own
`structure.yaml` needs to add these paths (or an equivalent) itself for units
and rules to be writable at all.

## Tests

`tests/` is the plugin's own end-to-end module, installing this plugin's real
`.sloprail/` tree into the mock harness exactly as `sloprail-tasks/tests/`
does. See `tests/README.md`.
