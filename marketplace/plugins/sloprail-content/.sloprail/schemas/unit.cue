// The frontmatter every memories/topics/<topic>/units/<NN_unit>/UNIT.md may
// carry. SHIPPED BY THE PLUGIN but read from the CONSUMER's project at
// $SR_WORKSPACE/.sloprail/schemas/unit.cue — installing the plugin means placing
// this file there, the same as sloprail-tasks's task.cue.
//
// BACKWARD COMPATIBLE with the pre-existing UNIT.md shape
// (memories/topics/<topic>/units/<NN_unit>/UNIT.md: transcript_path, created,
// type, status — see the consumer's document-topic skill). Every field this
// schema adds is OPTIONAL, and an absent `channels` means only the GLOBAL rules
// apply (a rule with no `applies_to.channels` selector) — a unit written before
// this plugin existed still validates and still gets judged, just against a
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
// (unit-publish-approved) refuses to reach without approved: + published_url:.
status?: "raw" | "drafting" | "published" | "parked"

// THE EXISTING CONTENT-SHAPE FIELD, unchanged. Doubles as a TAXONOMY axis: a
// rule may select on it (`applies_to: {type: [thread]}`), same as channels/tags.
type?: "post" | "thread" | "wedge" | "video"

// WHERE THIS TRACES BACK TO. Carried over from the existing shape; every UNIT.md
// already has one (enforced by the consumer's own PreToolUse hook, outside this
// plugin's remit).
transcript_path?: string
created?: string

// ============================================================ THE TAXONOMY ===
//
// Three axes a rule's `applies_to:` selects over (see rule.cue). A rule with NO
// selector is global and applies to every unit. This is the field the
// unit-satisfies-rules guard reads to decide which rules are IN SCOPE for a
// given unit — the taxonomy lives here, in the unit, not in the guard.
//
//   channels   WHERE it is going out — x, reddit, hn, github-readme,
//              github-issue-reply, influencer-brief, video, … An open string
//              list (not an enum): a project adds a channel by using it in a
//              unit's frontmatter and a rule's applies_to, no schema edit. The
//              plugin ships script rules keyed to specific channel names (x,
//              hn, reddit — see scripts/README); an unrecognised channel simply
//              has no script rule bound to it, which is not an error.
//   type       the EXISTING field above, reused as a taxonomy axis.
//   tags       FREE topical tags — a rule can select on these for anything the
//              channel/type axes don't capture (e.g. tags: [technical-deep-dive]
//              getting a "cite your sources" rule).
//
// A unit with no channels/tags at all — the pre-existing shape — still has
// `type` (already mandatory in practice) and gets every rule whose applies_to
// is empty or names only a type it has. This is the "missing channels means
// only global rules apply" compatibility the task spec asks for.
channels?: [...string]
tags?:     [...string]

// ==================================================== THE PUBLISH GATE =====
//
// A unit may not reach `status: published` without BOTH of these — enforced by
// unit-publish-approved, not by this schema (CUE sees a string; whether it
// GROUNDS to a real user message is a transcript question). Both optional here
// for the same reason task.cue's observations/artifacts are optional: mandatory
// only in one status, which CUE cannot express without turning every other
// status into a unification conflict.
//
// approved — a body-style citation LINK, `[quote](jsonl)`, exactly the shape
// sloprail-tasks's task body cites the ask with. It must GROUND against the
// `user` pool (`sr-session trajectory cite --source-types user`) — a real user
// message approving this unit for publication. An agent's own prior turn, a
// tool result, or a harness-injected message does not ground, so an agent
// cannot write its own approval. Held as a STRING (not a list): one unit, one
// publish decision, one citation of the approval that authorized it.
approved?: string

// published_url — where it actually went out. A plain string (not validated
// against a URL grammar here; the gate only requires it be non-empty) because
// the channel decides the shape (a tweet permalink, a GitHub PR URL, a
// markdown-rendered gist) and this schema does not know every channel's URL
// form.
published_url?: string
