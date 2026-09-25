// The frontmatter every memories/topics/<topic>/units/<NN_unit>/UNIT.md may
// carry. SHIPPED BY THE PLUGIN but read from the CONSUMER's project at
// $SR_WORKSPACE/.sloprail/schemas/unit.cue — installing the plugin means placing
// this file there, the same as sloprail-tasks's task.cue.
//
// BACKWARD COMPATIBLE with the pre-existing UNIT.md shape
// (memories/topics/<topic>/units/<NN_unit>/UNIT.md: transcript_path, created,
// type, status — see the consumer's document-topic skill). Every field this
// schema adds is OPTIONAL, and an absent `tags` means only the GLOBAL rules
// apply (a rule with no `applies_to` selector) — a unit written before this
// plugin existed still validates and still gets judged, just against a
// smaller rule set. Nothing already on disk needs to be touched to adopt this
// schema.
//
// NOT closed. Unlike task.cue, this schema does not wrap its fields in close():
// a UNIT.md is authored by a human/agent working FREELY in a topic (pitches,
// notes, whatever the document-topic skill's own template adds), and closing it
// would fight that. The taxonomy and publish-gate fields below are the ones
// THIS plugin's guardrails read; nothing stops a project from carrying more.

// WHERE THE UNIT IS IN ITS LIFECYCLE. The existing four values, unchanged —
// this plugin adds no new status. `published` is the one the publish gate
// (unit-publish-approved) refuses to reach without a grounded approval quote
// in the BODY (not a frontmatter field — see below) plus published_urls:.
status?: "raw" | "drafting" | "published" | "parked"

// THE EXISTING CONTENT-SHAPE FIELD, unchanged. NOT itself a taxonomy axis a
// rule selects on (that is tags, below) — it is metadata about the unit's own
// shape.
type?: "post" | "thread" | "wedge" | "video"

// WHERE THIS TRACES BACK TO. Carried over from the existing shape; every UNIT.md
// already has one (enforced by the consumer's own PreToolUse hook, outside this
// plugin's remit).
transcript_path?: string
created?: string

// ============================================================ THE TAXONOMY ===
//
// tags is the ONE selector axis a rule's `applies_to:` matches against (see
// rule.cue) — simplified from an earlier channels/type/tags split. A channel
// is just a tag: a unit going out on X carries `tags: [x]`, one on Reddit
// `tags: [reddit]`, and a unit can carry several (multi-channel, or a
// channel tag plus a topical one like `technical-deep-dive`). A rule with NO
// `applies_to` is global and applies to every unit regardless of tags.
//
// A unit with no tags at all — the pre-existing shape — still gets every
// rule whose applies_to is empty (global). This is the "missing tags means
// only global rules apply" compatibility the task spec asks for.
tags?: [...string]

// ==================================================== THE PUBLISH GATE =====
//
// A unit may not reach `status: published` without BOTH — enforced by
// unit-publish-approved, not by this schema:
//
//   1. a GROUNDED APPROVAL, cited in the unit's BODY, not frontmatter — the
//      same `[quote](jsonl)` shape and the same `cite --source-types user`
//      grounding a task body's ask uses. Deliberately NOT a frontmatter
//      field: grounding lives with the prose it grounds, exactly like a
//      task's ask citation, so there is one place (the body) a reader checks
//      for "what did the human actually say", not two. unit-publish-approved
//      reads the body for an approval-shaped citation link the same way
//      task-body-is-human-authored reads a task body for its ask citation.
//   2. published_urls: below.
//
// published_urls — where it actually went out. A LIST, not a single string:
// one unit may be distributed across several channels (an X post AND a
// cross-post to Reddit, say), each with its own URL. A plain string per
// entry (not validated against a URL grammar here; the gate only requires
// the list be non-empty) because the channel decides the shape (a tweet
// permalink, a GitHub PR URL, a markdown-rendered gist) and this schema does
// not know every channel's URL form.
published_urls?: [...string]
