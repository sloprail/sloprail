// The frontmatter every memories/topics/<topic>/units/<NN_unit>/UNIT.md may
// carry. Shipped by the plugin and read from the plugin's own tree (each guard
// resolves it from its folder, $SR_GUARDRAIL_DIR/../../schemas/unit.cue), the same
// as sloprail-tasks's task.cue: a consumer project copies nothing.
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
// (unit-publish-approved) refuses to move INTO without the user's approval
// cited on the change itself (not stored in the unit — see below), and
// refuses to hold without published_urls:.
status?: "raw" | "drafting" | "published" | "parked"

// THE EXISTING CONTENT-SHAPE FIELD, unchanged. NOT itself a taxonomy axis a
// rule selects on (that is tags, below) — it is metadata about the unit's own
// shape.
type?: "post" | "thread" | "wedge" | "video"

// WHERE THIS TRACES BACK TO. Carried over from the existing shape; every UNIT.md
// already has one (enforced by the consumer's own PreToolUse hook, outside this
// plugin's remit). Accepted for compatibility only: no guard in this plugin
// reads it, and nothing here grounds anything in it — grounding rides on the
// sr-file command that makes a change (see THE PUBLISH GATE below).
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
// Enforced by unit-publish-approved, not by this schema:
//
//   1. THE USER'S APPROVAL, CITED ON THE ACTION — not a field, and not text in
//      the body. A write that moves a unit INTO status: published (from any
//      other status, or from no file) must carry a citation of the user's own
//      words, e.g.
//        sr-file edit UNIT.md --old-string 'status: drafting' --new-string 'status: published' --cite:user 'ship it'
//      The session resolves the quote against its own record; the unit keeps
//      no approval quote and no transcript link (neither would resolve on
//      another machine). An earlier draft's approval field, and after it an
//      approval link in the body, are both gone.
//   2. published_urls: below, whenever the unit is at status: published.
//
// published_urls — where it actually went out. A LIST, not a single string:
// one unit may be distributed across several channels (an X post AND a
// cross-post to Reddit, say), each with its own URL. A plain string per
// entry (not validated against a URL grammar here; the gate only requires
// the list be non-empty) because the channel decides the shape (a tweet
// permalink, a GitHub PR URL, a markdown-rendered gist) and this schema does
// not know every channel's URL form.
published_urls?: [...string]
