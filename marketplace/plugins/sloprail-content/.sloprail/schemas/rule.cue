// The frontmatter every writing RULE carries — a
// `.sloprail/content-rules/<NN_name>/RULE.md` (project-wide) or a topic's own
// `memories/topics/<topic>/constraints/<NN_name>/CONSTRAINT.md` (topic-scoped;
// the pre-existing shape, migrated in place — see the plugin README's migration
// note). Read by unit-satisfies-rules's prepare.sh via `sr-file validate --emit`,
// the same way task.cue's frontmatter is read.
//
// CLOSED, and closed EXPLICITLY — a rule whose frontmatter carries a key this
// schema does not name is a load-time validation error, not a silently accepted
// extension. Mirrors task.cue's own close() and its stated reason: an unclosed
// schema was measured to accept an invented key silently.
close({

// LEVEL — both hard, no advisory tier. Identical semantics to the pre-existing
// CONSTRAINT.md's level: `must` (the unit MUST satisfy this), `must_not` (the
// unit MUST NOT). A "should" that cannot refuse is a comment, not a rule.
level!: "must" | "must_not"

created?: string

// ============================================================ THE TAXONOMY ===
//
// tags SELECTS which units this rule is checked against — a unit's frontmatter
// tags. NO tags at all (tags entirely absent) means GLOBAL — every unit,
// regardless of its own tags. Present, it is a SUBSET test: the rule's listed
// tags must intersect the unit's own tags.
//
// Simplified from an earlier channels/type/tags selector to just tags: a
// channel is a tag like any other ("x", "reddit", "hn"), and a unit's `type`
// is not itself a taxonomy axis a rule selects on — tag the unit if a rule
// should apply to it. `applies_to: [x]` is exactly "the X rules"; no
// `applies_to` at all is exactly "the global style rules". Topic-scoped rules
// (the migrated constraints/) carry NO applies_to at all — their scope is
// already the topic they live under (see unit-satisfies-rules/prepare.sh), so
// the selector would be redundant; if one is present anyway it is honoured as
// an ADDITIONAL filter within that topic.
applies_to?: [...string]

})

// ================================================================ GROUNDING ===
//
// A rule's ORIGIN is not a frontmatter field (an earlier draft's
// transcript_paths was dropped — see the plugin README's migration note). A
// rule grounds itself the SAME WAY a task's body grounds its ask
// (sloprail-tasks's task-body-is-human-authored): the RULE'S BODY carries at
// least one `[quote](jsonl)` markdown link whose quote is the user's own
// words and resolves via `sr-session trajectory cite --source-types user`,
// checked by content-rule-is-grounded's script stage, then a judge confirms
// the rule states only what the cited quote(s) say and nothing invented. No
// separate schema field for this — the body IS the citation, exactly as a
// task body's ask citation is.
