# sloprail-content

Guardrails for a `memories/topics/<topic>/units/` content workflow — a unit's
own **tags** decide which writing rules apply, a **judge** weighs every rule
(including the mechanically countable ones — character limits, banned
phrases — which are rules written to tell the judge exactly what to measure
and how, using Bash), and a unit cannot move into `status: published` unless
the change that moves it **cites the user's approval**. Rules and approvals
are grounded on the ACTION — `sr-file write|edit ... --cite:user '<exact
quote>'` at a pre-write gate, or a `Sloprail-Cites-User: <exact quote>` trailer
on the commit that carries the change, which is what the file-guards read —
never by storing a transcript quote or path in the file, so the repository
holds derived text only. The file-guards judge **commits**: at Stop, uncommitted
changes to a file they select refuse the turn with "commit these", and each rule
is then judged by `sr-checks run` over the range `merge-base(base, HEAD)..HEAD`. A unit folder is not a unit until its
`UNIT.md` exists — nothing else may be written there first. Four guardrails, in the nature format —
file-guards under `.sloprail/`, with `match:` / `checks:`, flat `.event`
fields, and refusals delivered as a non-zero exit carrying `{"reason": …}` on
stdout.

## The taxonomy

A unit's frontmatter (the plugin's `.sloprail/schemas/unit.cue`) carries one selector:
`tags`, an open string list, backward compatible with the pre-existing
`UNIT.md` shape — a unit with no `tags` still validates and still gets every
rule whose selector is empty. A channel is just a tag (`x`, `reddit`, `hn`,
`github-readme`, …); so is anything topical (`technical-deep-dive`). A unit
may carry several, including more than one channel if it is cross-posted.

Example unit frontmatter:

```yaml
---
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
Migration below), frontmatter validated against the plugin's `.sloprail/schemas/rule.cue`:

```yaml
---
level: must_not
created: 2026-09-25
applies_to: [x]
---
Rule: no rhetorical questions as openers ("Ever wonder why...?").

PASS: "I shipped X because Y broke in prod."
FAIL: "Ever wonder why your builds keep failing?"
```

- `level`: `must` or `must_not`, both hard — there is no advisory tier.
- `applies_to`: the tag selector above; absent = global.
- **Every rule is a judge rule** — the body above is put to a model. There is
  no separate script-rule mechanism (see "The mechanism" below).

### Grounding — on the action, not in the file

A rule's origin is not stored in the rule. Every create or edit of a
`RULE.md` / `CONSTRAINT.md` is made with `sr-file`, citing the user's own
words on the command:

```bash
sr-file write .sloprail/content-rules/03_openers/RULE.md --cite:user 'never open with a rhetorical question' <<'EOF'
---
level: must_not
applies_to: [x]
---
Rule: no rhetorical questions as openers.
EOF
```

Run `sr-file` on its own in the command line (only `sr-file` calls, `&&`,
`echo`, heredocs), so its result is known before it runs. The session
resolves each quote against its own record before any guard sees the change;
a quote the user never said is no citation. The Write and Edit tools cannot
carry a citation, so `content-rule-is-grounded` refuses them on a rule file
and says how to redo the change. The file keeps only the derived rule text.

Earlier versions of this plugin stored the origin in the file, first as a
`transcript_paths` frontmatter list, then as a `[quote](/abs/session.jsonl:N)`
link in the body. Neither resolves on another machine, so both are gone. A
rule that still carries such a link keeps working: the link is neither
required nor refused, and the next change to that rule is grounded the new
way.

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

### unit-md-first — gate + file-guard

Over any file under `memories/topics/<topic>/units/<unit>/` other than
`UNIT.md` itself. A unit folder with no `UNIT.md` declares no `type`,
`status` or `tags`, so `unit-satisfies-rules` and `unit-publish-approved`
have nothing to select and the file is orphaned — seen in practice, an agent
wrote a draft into a unit folder before its `UNIT.md` existed. The check is
purely path-based: it refuses the write unless `<unit folder>/UNIT.md`
already exists on disk, and says to write that first. It is a `PreFileWrite`
gate (refuses before the write lands) plus a plain file-guard of the same name
that re-checks the committed files (`sr-checks run` judges, Stop verifies) (a `UNIT.md` still uncommitted does not
count: the file-guard reads the committed head). Deletions are not this rule's
business (no `PreFileDelete` trigger; the file-guard skips deletions).

Proven first as a project-local rule in the strategy repo's own
`.sloprail/file-guard/unit-md-first/` before being generalized here so every
project installing this plugin gets it.

### unit-satisfies-rules — file-guard, Stop after-check

On a unit's `UNIT.md` or `02_draft.md` in the committed range: for each unit the
range touches, collects every rule the unit's own tags select (from its committed
`UNIT.md`, so a change to only the draft is judged against the tags its `UNIT.md`
carries) (project-wide + its topic's `constraints/`), and sends all of
them in one call to the engine's judge (`model: size-md` — the cheapest
adequate tier for a real judgement call, not the largest default and not the
smallest; `allowed_tools: [Read, Bash]` so a deterministic rule's own
measurement instruction can actually be run). Refuses on any failing
`must`/`must_not`, naming the rule folder, its level, and why (including the
measured number for a deterministic rule). A unit selecting no rules at all
(no global rules configured, no matching tag rule, no topic constraints)
passes trivially — nothing to check is not a violation.

### unit-publish-approved — gate + file-guard

Over a unit's `UNIT.md` only. Publishing needs **both**:

- **The user's approval, cited on the change that publishes.** A change that
  moves a unit INTO `status: published` — from any other status, or by
  creating it published — must carry at least one citation in the `user`
  pool: the user's own words approving it. Ask the user; once they approve,
  publish with `sr-file` (the gate reads that citation):

  ```bash
  sr-file edit memories/topics/20260101_launch/units/01_announce/UNIT.md \
    --old-string 'status: drafting' \
    --new-string 'status: published
  published_urls: ["https://x.com/nikita/status/1234567890"]' \
    --cite:user 'ship it'
  ```

  The file-guard reads the same approval from the commit that carries the publish:
  `git commit -m 'Publish the launch post' -m 'Sloprail-Cites-User: ship it'`.

  The session resolves the quote against its own record. An agent's own
  prior turn, a tool result, or a harness-injected message is not in the
  `user` pool, so an agent cannot cite its own output as the approval that
  authorizes itself to publish. The unit stores no approval text and no
  transcript link. A write that is not a transition into published (a
  draft edit, an edit to an already-published unit) needs no citation.
- **`published_urls:`** (frontmatter), whenever the unit is at
  `status: published` — where it actually went out, as a **list**: one unit
  may be distributed across several channels (posted to X and cross-posted
  to Reddit, say), each with its own URL.

The status is read from the frontmatter as written, not through `unit.cue`:
one reader, `publish-claim.sh`, shared by the `when` and the check, asks
`sr-file field <UNIT.md> status` — a plain YAML reader, so valid YAML the schema
would reject (an integer key, a custom tag) still answers.

- A write that claims `status: published` and breaks the schema somewhere else
  (`type: article`) still needs the user's cited approval.
- **Any** write that leaves a unit at `status: published` with frontmatter
  invalid against `unit.cue` is refused, including an edit to a unit that was
  already published (which needs no new approval, but must stay valid).
- Frontmatter that cannot be read — YAML that does not parse, more than one
  YAML document, `status` (or a `<<` merge key) defined twice — cannot be read
  for a status, so it is treated as a claim to publish: the approval is
  required, and the write is refused until the frontmatter reads.
- A `---` fence that is never closed is read too — the whole text under it
  first, then, if that does not parse, its first paragraph (where a forgotten
  frontmatter would be): YAML claiming `status: published` is treated as a claim
  to publish (a reader that tolerates a missing close would publish it), and so
  is text that does not parse but names a `status` key. Prose under a
  horizontal rule — however many colons it has — claims nothing. A frontmatter placed after a
  byte-order mark or blank lines is treated the same way: harmless unless it
  claims published. A file with no frontmatter at all claims nothing.
- The status is read with `sr-file field`, which first ships in the sloprail
  release after 0.2.1, so the sloprail binaries must be newer than 0.2.1.
  Against an older `sr-file` every unit write is refused with a message naming
  the installed version and the upgrade (`install.sh`, or
  `make distribute-local`). The marketplace serves this plugin from the default
  branch while `install.sh` installs the latest release, so until a release
  newer than 0.2.1 is out, marketplace users get that refusal.
- A unit that is not published is not this guard's business, valid or not.

A broken field is never a way to publish unchecked.

Publishing is the irreversible step, and the task is explicit that an agent
must not be able to publish on its own say-so — so this rule, like
`content-rule-is-grounded` below, is a `PreFileWrite` gate (it refuses before
the write lands, and refuses a write whose result it cannot derive, such as
`sed -i`) plus a plain file-guard of the same name. The file-guard is the Stop
after-check and the backstop: there, "before" is the range's base (the merge base, or the
rule-age floor when later), and the citations are the ones the range's commits carry
(`Sloprail-Cites-User:` trailers, resolved against the transcripts). The range is one net
change, so it carries all its commits and their citations. `enters-published.sh` says whether any unit in the changeset entered
`published`; if so, the range must cite the user. So a publish that slipped
through uncited is still refused, with the steps to redo it. The
citation is required only on the transition, so the guard declares it with a
`when:` script — a draft edit needs none:

```yaml
require:
  - citation: {source_types: [user]}
    when: ./enters-published.sh
```

### content-rule-is-grounded — gate + file-guard

Protects the RULE SET itself — the same role `task-body-is-human-authored`
plays for a task's ask. Grounding is unconditional: every create or update of
a rule must cite the user. A `PreFileWrite` gate refuses an ungrounded rule
before it lands (and refuses a write whose result it cannot derive, such as one
mixing `sr-file` with another program); a plain file-guard of the same name runs
the same require, script and judge on the committed rules (`sr-checks run` judges, Stop verifies).

0. **`require: [{citation: {source_types: [user]}}]`.** A change carrying no citation that
   resolves to the user's own words is refused by the engine before any
   check runs: for the gate a remedy naming `sr-file ... --cite:user`, for the
   file-guard commits without a `Sloprail-Cites-User:` trailer. The Write and
   Edit tools, and a quote the user never said, are refused here.
1. **Script (`check-rule.sh`), deterministic, fail-closed.** The frontmatter
   satisfies `rule.cue` and the body is not empty.
2. **Prepare + judge (`resolve-cited-rule-quotes.sh` +
   `judge-rule-body.md.j2`).** The prepare hands the judge the cited quotes
   (with their transcript path and line) off `changeset.citations`, each rule's
   body, and the change (one diff of every rule in the range). The rule must
   correspond to the cited words and hold that and nothing else — a valid
   citation wrapped in agent-invented scope, threshold, exception or
   rationale the user never stated is refused. On an update, only what the
   change adds or alters is judged against the cited words.

Deleting a rule is not guarded (no `PreFileDelete` trigger; the file-guard
skips deletions): removing a rule invents nothing.

An earlier draft also checked that a rule's `script:` field named a script
capable of refusing. That check is gone along with `script:` itself — every
rule is a judge rule now, so a rule that can never actually refuse is
exactly what stage 2's "traceable and substantive" judgement already catches.

## Migration from the topic-local `unit-satisfies-constraints` guard

If a project (like `strategy`) already has the old, topic-only guard:

1. Install this plugin. Its `unit.cue` and `rule.cue` ship in the plugin's own
   `.sloprail/schemas/` and are read from there; the project copies nothing.
2. **`constraints/` needs no changes to its location or filename.**
   `unit-satisfies-rules`'s `rules-lib.sh` collects a unit's topic
   constraints the exact same way the old guard's `prepare.sh` did (same
   directory, same filename, same `level`/PASS-FAIL shape). What DOES change
   going forward: nothing in a `CONSTRAINT.md` is read for grounding any
   more (not an old `transcript_paths:` field, not a body link). Instead
   every WRITE to one (create or edit) must be made with `sr-file ...
   --cite:user '<exact quote>'`. Installing this plugin does not
   retroactively walk existing files, so an old `CONSTRAINT.md` keeps working
   as it is; its old link or field can stay. This is deliberate: the guard
   protects new authoring, not a one-time migration audit.
3. Add project-wide rules under `.sloprail/content-rules/` for anything that
   should apply across topics by tag — the X rules, the global style rules,
   the deterministic limits — which is the actual generalization this plugin
   adds.
4. Remove the old `.sloprail/file-guard/unit-satisfies-constraints/` folder
   once satisfied the new guard covers the same ground (or disable it via
   `.sloprail/config.yaml`'s `disabled:` list first, to compare side by side).
5. Stop storing publish approvals in units: an approval is now cited on the
   `sr-file` command that moves the unit to `status: published` (an earlier
   `approved:` frontmatter field, and after it an approval link in the body,
   are both gone; leftover ones are ignored). Add `published_urls:` (a list;
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
