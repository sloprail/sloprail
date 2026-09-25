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

// WHERE THIS RULE CAME FROM — traceable, exactly like CONSTRAINT.md's existing
// transcript_paths. Not grounded/verified by any guardrail (unlike a task body's
// citation or the publish gate's approved:) — this is PROVENANCE, an audit trail
// for "why does this rule exist", not a claim this plugin checks.
transcript_paths?: [...string]

created?: string

// ============================================================ THE TAXONOMY ===
//
// applies_to SELECTS which units this rule is checked against, over the SAME
// three axes unit.cue's frontmatter carries: channels, type, tags. NO selector
// at all (applies_to entirely absent) means GLOBAL — every unit, regardless of
// its own frontmatter. Each axis present in applies_to is OR'd within itself and
// AND'd across axes: `applies_to: {channels: [x, reddit], type: [post]}` means
// "a unit whose channels include x OR reddit, AND whose type is post". An axis
// unit-satisfies-rules cannot find a value for is a SUBSET test: the rule's
// listed values must intersect the unit's own list (channels/tags) or equal its
// scalar (type).
//
// A rule with applies_to.channels: [x] is exactly "the X rules"; one with no
// applies_to at all is exactly "the global style rules" from the task's own
// framing. Topic-scoped rules (the migrated constraints/) carry NO applies_to
// at all — their scope is already the topic they live under (see
// unit-satisfies-rules/prepare.sh), so the selector would be redundant; if one
// is present anyway it is honoured as an ADDITIONAL filter within that topic.
applies_to?: close({
	channels?: [...string]
	type?: [...("post" | "thread" | "wedge" | "video")]
	tags?: [...string]
})

// ============================================================ THE MECHANISM ===
//
// A rule is EITHER a judge rule (the rule text below is put to a model — style,
// tone, positioning, anything that is a judgement call) OR a script rule (a
// NAMED script the plugin or the project ships, plus its arguments — for
// anything deterministic, because an LLM is bad at counting characters). Never
// both, and never inline shell: a script rule names a script, it does not carry
// one.
//
// script ABSENT means this is a JUDGE rule: the body (rule text + PASS/FAIL,
// read off the .md by unit-satisfies-rules, not this schema) is the judge's
// rubric for this one rule.
//
// script PRESENT means this is a SCRIPT rule: `name` is one of the scripts this
// plugin ships under scripts/ (see scripts/README.md — char-limit, hn-title-
// limit, reddit-title-limit, banned-phrases) or a project-local one under the
// consumer's own `.sloprail/content-rules/scripts/`, resolved the same way;
// `args` are passed to it verbatim. The body of a script rule's .md is
// documentation for a human, never executed.
script?: close({
	name!: string
	args?: [...string]
})

})
