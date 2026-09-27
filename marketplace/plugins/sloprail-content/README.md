# sloprail-content

Guardrails for a `memories/topics/<topic>/units/` content workflow — a unit's
own **tags** decide which writing rules apply, a **judge** weighs every rule
(including the mechanically countable ones — character limits, banned
phrases — which are rules written to tell the judge exactly what to measure
and how, using Bash), and a unit cannot reach `status: published` without a
**cited user approval** in its body. Three guardrails, in the nature format —
file-guards under `.sloprail/`, with `match:` / `checks:`, flat `.event`
fields, and refusals delivered as a non-zero exit carrying `{"reason": …}` on
stdout.

## The taxonomy

A unit's frontmatter (`.sloprail/schemas/unit.cue`) carries one selector:
`tags`, an open string list, backward compatible with the pre-existing
`UNIT.md` shape — a unit with no `tags` still validates and still gets every
rule whose selector is empty. A channel is just a tag (`x`, `reddit`, `hn`,
`github-readme`, …); so is anything topical (`technical-deep-dive`). A unit
may carry several, including more than one channel if it is cross-posted.

Example unit frontmatter:

```yaml
---
transcript_path: /Users/nikita/.claude/projects/.../abc.jsonl
created: 2026-09-25
type: thread
status: drafting
tags: [x, launch]
---
```

A rule declares `applies_to:` as a bare tag list; **no selector at all**
means global (every unit). Present, it is a subset test — the rule applies
when ANY of its listed tags is one of the unit's own:

```yaml
applies_to: [x, reddit]   # the unit's tags must include x OR reddit
```

So `applies_to: [x]` is "the X rules", and no `applies_to` at all is "the
global style rules" — exactly the framing the taxonomy was asked for. (An
earlier draft split the selector into `channels`/`type`/`tags` axes; this was
simplified to just tags — a channel is a tag like any other, and `type`
[post/thread/wedge/video] stays on the unit as content-shape metadata but is
not itself a selector a rule matches on.)

## The rule format, and where rules live

A rule is a `RULE.md` (project-wide) or `CONSTRAINT.md` (topic-scoped, see
Migration below), frontmatter validated against `.sloprail/schemas/rule.cue`:

```yaml
---
level: must_not
created: 2026-09-25
applies_to: [x]
---
Rule: no rhetorical questions as openers ("Ever wonder why...?"). The user
said: [never open with a rhetorical question](/abs/session.jsonl:88).

PASS: "I shipped X because Y broke in prod."
FAIL: "Ever wonder why your builds keep failing?"
```

- `level`: `must` or `must_not`, both hard — there is no advisory tier.
- `applies_to`: the tag selector above; absent = global.
- **Every rule is a judge rule** — the body above is put to a model. There is
  no separate script-rule mechanism (see "The mechanism" below).

### Grounding — the body, not a frontmatter field

A rule's origin is NOT a frontmatter field. An earlier draft carried a
`transcript_paths` list; that is gone. A rule grounds itself the same way a
task's body grounds its ask (`sloprail-tasks`'s own
`task-body-is-human-authored`): the rule's **body** carries at least one
`[quote](jsonl)` markdown link whose quote is the user's own words and
resolves via `sr-session trajectory cite --source-types user`. The
`content-rule-is-grounded` guard checks this — see below — reusing that
plugin's pattern exactly rather than reinventing citation grounding.

### The mechanism — every rule is a prompt, deterministic ones included

An earlier draft of this plugin shipped `.sloprail/scripts/` — hand-written
`.sh` scripts for character limits and a banned-phrase list, dispatched by a
`script:` field on the rule. That machinery is gone entirely. A rule that
needs a *deterministic* check (a character limit, a title-length cap, a
banned-phrase or regex match) is written as **rule text that tells the judge
exactly what to measure and how**, and the judge is granted the `Bash` tool
so it can actually run the measurement — `wc -c`, `grep`, whatever the rule
specifies — against the real unit file, rather than eyeball it:

```yaml
---
level: must
applies_to: [x]
---
Rule: each tweet in an X post/thread is at most 280 characters. If the unit
is a thread, tweets are split by a line containing exactly `---`. Measure
each segment's character count with `wc -m` (not bytes) against the unit
file; do not estimate.
```

```yaml
---
level: must_not
applies_to: []
---
Rule: no AI-sounding hedge phrases — "it's worth noting", "in today's
landscape", "delve into", or three-or-more em-dashes in one unit. Check with
grep against the unit file; do not eyeball it.
```

This is a deliberate collapse of "deterministic vs. judgement-call" into one
mechanism: "deterministic" is a property of how precisely a rule's own text
is written plus the judge's tool access (`allowed_tools: [Read, Bash]` on
`unit-satisfies-rules`'s check), not a second check kind with its own script
dispatch, install path, and structure-gate carve-out to maintain.

### Where rules live in the consumer project

Two locations, both collected for every unit and merged into one applicable
set:

1. **Project-wide**, `.sloprail/content-rules/<NN_name>/RULE.md` — selected by
   `applies_to`. This is new with this plugin: a tag-selected rule that
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
own tags select (project-wide + its topic's `constraints/`), and sends all of
them in one call to the engine's judge (`model: size-md` — the cheapest
adequate tier for a real judgement call, not the largest default and not the
smallest; `allowed_tools: [Read, Bash]` so a deterministic rule's own
measurement instruction can actually be run). Refuses on any failing
`must`/`must_not`, naming the rule folder, its level, and why (including the
measured number for a deterministic rule). A unit selecting no rules at all
(no global rules configured, no matching tag rule, no topic constraints)
passes trivially — nothing to check is not a violation.

**Tags always come from the unit's own `UNIT.md`, and the draft is what gets
judged.** `02_draft.md` is prose with no frontmatter of its own, so tags are
never read off whichever file the write event named — they are read from
`UNIT.md` (on disk, or the settled write itself when the event IS `UNIT.md`).
Whichever of the two files triggered the check, if `02_draft.md` exists on
disk it is the content judged against the tag-selected rules (a writing rule
is about what will ship). A `UNIT.md`-only write with no draft yet has
nothing shippable to judge yet and passes trivially, the same as selecting no
rules — once a draft exists, the next write judges it for real.

**Deterministic facts are pre-computed, not left to the judge's own Bash
loop.** `prepare-judge-rules.sh` runs `compute-draft-facts.sh` against the
judged content once, cheaply, and hands the judge title-line character
count, body word count (frontmatter/title excluded), em-dash count, emoji
count, and each thread segment's own character count (split on a line
containing exactly `---`) as ready-made numbers. The judge template tells
the judge to use these first and reach for its own `Read`/`Bash` measurement
only when a rule needs something these facts do not cover — and separately,
to judge only what a rule's own text states: no fact-checking a unit's
claims, no exploring the repo beyond `additionalContext.judged_path`, unless
a rule's text explicitly asks for it (e.g. comparing against a sibling
unit).

### unit-publish-approved — file-guard, **preventive**

A unit cannot reach `status: published` without **both**:

- a **grounded approval quote in the body** — a `[quote](jsonl)` citation
  link, the *exact* shape and grounding mechanism `sloprail-tasks`'s task
  body uses for the human's ask. The quote must resolve, via `sr-session
  trajectory cite --source-types user`, to a **real user message**. An
  agent's own prior turn, a tool result, or a harness-injected message
  (`<system-reminder>`, `<task-notification>`, …) does not ground — `cite`
  excludes all three by construction — so an agent cannot cite its own
  output as the approval that authorizes itself to publish. This is
  deliberately in the BODY, not frontmatter (an earlier draft's `approved:`
  field moved here): grounding lives with the prose it grounds, the same way
  a task's ask citation does, so there is one place — the body — a reader
  checks for "what did the human actually say."
- `published_urls:` (frontmatter) — where it actually went out, as a
  **list**: one unit may be distributed across several channels (posted to
  X and cross-posted to Reddit, say), each with its own URL.

```yaml
---
type: post
status: published
published_urls: ["https://x.com/nikita/status/1234567890"]
---

## Approval
The user said: [go ahead, ship it](/Users/nikita/.claude/projects/.../abc.jsonl:42)
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
plays for a task's ask, reusing its exact two-stage pattern for a rule's body
instead of a task's:

1. **Script (`check-rule.sh`), deterministic, fail-closed.** The rule's body
   must carry at least one `[quote](jsonl)` link whose quote GROUNDS via
   `cite --source-types user` to the user's own words. No citation, or one
   that does not ground, is refused here, before the judge.
2. **Prepare + judge (`resolve-cited-rule-quotes.sh` +
   `judge-rule-body.md.j2`).** The rule must correspond to the cited words and
   hold that and nothing else — a valid citation wrapped in agent-invented
   scope, threshold, exception or rationale the user never stated is refused,
   the same "and nothing else" standard `task-body-is-human-authored`'s own
   judge applies to a task's ask.

An earlier draft also checked that a rule's `script:` field named a script
capable of refusing. That check is gone along with `script:` itself — every
rule is a judge rule now, so a rule that can never actually refuse is
exactly what stage 2's "traceable and substantive" judgement already catches.

## Migration from the topic-local `unit-satisfies-constraints` guard

If a project (like `strategy`) already has the old, topic-only guard:

1. Install this plugin — `unit.cue` and `rule.cue` under the project's
   `.sloprail/schemas/`.
2. **`constraints/` needs no changes to its location or filename.**
   `unit-satisfies-rules`'s `rules-lib.sh` collects a unit's topic
   constraints the exact same way the old guard's `prepare.sh` did (same
   directory, same filename, same `level`/PASS-FAIL shape). What DOES change
   going forward: an existing `CONSTRAINT.md`'s old `transcript_paths:`
   frontmatter field is no longer read for grounding — `content-rule-is-
   grounded` reads a `[quote](jsonl)` link in the BODY instead. **This is
   checked only on a `CONSTRAINT.md`'s next WRITE** (create or edit) —
   installing this plugin does not retroactively walk existing files, so an
   old `CONSTRAINT.md` with no body citation keeps working until someone
   next edits it, at which point it must carry a real, resolving body
   citation to pass. This is deliberate: the guard protects new authoring,
   not a one-time migration audit.
3. Add project-wide rules under `.sloprail/content-rules/` for anything that
   should apply across topics by tag — the X rules, the global style rules,
   the deterministic limits — which is the actual generalization this plugin
   adds.
4. Remove the old `.sloprail/file-guard/unit-satisfies-constraints/` folder
   once satisfied the new guard covers the same ground (or disable it via
   `.sloprail/config.yaml`'s `disabled:` list first, to compare side by side).
5. Move any unit workflow's publish approval out of frontmatter and into the
   unit's body as a grounded citation quote, add `published_urls:` (a list;
   an earlier singular `published_url:` string is gone), and install
   `unit-publish-approved`.

No unit on disk needs other changes: `tags` is optional, and a unit with none
still gets every global rule and its topic's constraints, same as before this
plugin existed.

## The structure gate piece

`.sloprail/file-guard/structure.yaml` is this plugin's own contribution to the
structure gate: two `scope` folders it owns —

```yaml
scope:
  - glob: "memories/topics/"
  - glob: ".sloprail/content-rules/"
```

— plus an `allow` list naming exactly the TOPIC.md / UNIT.md / draft /
CONSTRAINT.md / distribution shapes under `memories/topics/`, and the RULE.md
shape under `.sloprail/content-rules/`. Per the base `sloprail` plugin's
structure-gate composition
(`marketplace/plugins/sloprail/skills/authoring-guardrails/structure-gate.md`):
inside these two folders, THIS plugin alone decides what may be written — a
project's own `allow` does not widen it, though a project's `deny` still
vetoes — and outside them this plugin's structure has no say at all. A
consumer project needs to do nothing extra for units and rules to be
writable; installing the plugin is enough. Validate what a plugin ships with:

```
sr-file declarations --plugin sloprail-content path/to/sloprail-content
```

## Tests

`tests/` is the plugin's own end-to-end module, installing this plugin's real
`.sloprail/` tree into the mock harness exactly as `sloprail-tasks/tests/`
does. See `tests/README.md`. It runs in this repo's CI under the `plugins`
e2e shard (`make test-plugins-e2e`, `.github/workflows/test.yml`) alongside
`sloprail-tasks/tests`.
