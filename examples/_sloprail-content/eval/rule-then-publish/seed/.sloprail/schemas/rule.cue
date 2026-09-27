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
// A rule's ORIGIN is not stored in the rule at all — neither a frontmatter
// field (an earlier draft's transcript_paths was dropped) nor a link to a
// transcript in the body (a transcript path does not resolve on any other
// machine). The citation rides on the ACTION: every create or update is made
// with `sr-file write|edit <path> --cite:user '<exact quote>'`, which
// content-rule-is-grounded requires (`require: [{citation: {source_types: [user]}}]`), and its
// judge then confirms the rule states only what the cited words say and
// nothing invented. The file keeps only the derived rule text. A body link
// left by an older rule is neither read nor refused.
