// The frontmatter every memories/tasks/<group>/<name>/TASK.md must carry.
//
// Checked by the task-frontmatter guardrail via `sr-file validate`, which reads
// the frontmatter of a .md and vets it as a CUE document.
//
// CLOSED — and closed EXPLICITLY, with the `close()` below, because CUE's
// default is open and a comment claiming otherwise is not a constraint. This
// was measured: before the definition was wrapped, a task carrying a `sneaky:`
// key it had invented validated cleanly.
//
// Closed, unlike this repo's artifact.cue. That schema is open because a topic
// and a decision carry per-kind frontmatter a common floor has no business
// enumerating. A task has no such variation: the fields below are all of them,
// and the whole point of the rule is that an agent cannot invent a sixth status
// or a seventh field to park a claim in. An open schema would admit
// `status: done_ish` under a different key and call it valid.

// Everything below is embedded in a close(), so a key this file does not name
// is a validation error rather than a silently accepted extension.
close({

// WHERE THE TASK IS IN ITS LIFECYCLE.
//
// A disjunction of five literals, not `string`. This is the field the rest of
// the system keys on — the stop rule reads it to decide whether a turn may end,
// and the review rule reads it to decide whether to spend a model call — so a
// typo here must be a load-time refusal rather than a task that silently stops
// counting.
//
// The order below is the order work moves through, and each is the answer to a
// different question:
//
//   backlog     — filed, not scheduled. Parked deliberately; the stop rule
//                 ignores it. This is where an ask goes when it is real but
//                 not now, and it exists so that "not now" has somewhere to
//                 live other than to_do, which would make to_do meaningless.
//   to_do       — scheduled, not started.
//   in_progress — being worked.
//   in_review   — the agent claims it is finished and has attached its
//                 evidence. NOT the agent's verdict: the claim is what this
//                 status records, and the review guardrail is what judges it.
//   blocked     — cannot proceed. The body must name the blocker.
//
// `done` is deliberately NOT in this list. A task the reviewer approves is
// DELETED, folder and all, by the reviewer itself — so `done` would be a state
// nothing is ever allowed to rest in, and a status that only ever exists for
// the instant before a deletion is not a status, it is a return value. Leaving
// it out means an agent cannot write it, which closes the exact hole the review
// rule exists for: an agent marking its own work complete.
status!: "backlog" | "to_do" | "in_progress" | "in_review" | "blocked"

// WHAT GETS DONE NEXT.
//
// P0 is in this list because this repo's own backlog uses it and its README
// defines it ("the engine does not work without it"). A schema that refused P0
// would refuse eleven existing tasks on the day it landed.
//
// P4 was added when the first task wanting it arrived — a whole product to
// package, real but further out than anything the existing tiers describe. It is
// added to the SCHEMA rather than worked around by filing such a task as P3,
// because a priority the schema does not know is a priority no task can carry,
// and squeezing it into P3 would make P3 mean two different distances at once.
//
// Note there is no definition of P4 here, deliberately. The README defines what
// the tiers MEAN for this backlog, and a schema that also defined them would be
// a second place to disagree with. This file says which strings are allowed.
priority!: "P0" | "P1" | "P2" | "P3" | "P4"

// THE EVIDENCE, REQUIRED ONLY IN `in_review`.
//
// Both are lists of `<path>:<ranges>` citations, and both are OPTIONAL here
// while being MANDATORY in in_review. That split is deliberate and the reason
// is which checker can ask which question.
//
// CUE can express "if status is in_review then these must be present" — but it
// cannot express what actually matters about them: that the path resolves, that
// the line range exists in the file, that the cited JSONL line is a real tool
// result rather than the agent's own prose. Those are filesystem questions, and
// a schema sees a string.
//
// So the shape is pinned here and the substance is checked in the hook, the
// same division artifact.cue draws for transcript_path: the schema says what a
// citation must LOOK like, and the guardrail says whether it is TRUE.
//
// The regex is the load-bearing half of this file after `status`. It demands a
// leading `/` — an absolute path, because a task is read by a reviewer whose
// working directory is nobody's business — then a colon, then at least one
// range. A range is `N` or `N-M`, and several are comma-separated, which is the
// spelling the user asked for.
// Defined at the top level, outside the closed struct: a definition is a schema
// construct rather than a field, and one written inside close() would be read as
// a key the document is not allowed to carry.
_citation: =~"^/[^:]+:[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$"

// PROOF THAT THE WORK HAPPENED — cited into the session record.
//
// Each entry is an absolute `.jsonl` transcript path with the line ranges of the
// entries that show the result: the test run that came back green, the command
// whose output is the evidence. Positions in a record the agent could not author
// after the fact, rather than a summary saying it went well — which is exactly
// what a model produces when it did not.
observations?: [..._citation]

// WHERE THE RESULT OF THE WORK IS — cited into the tree.
//
// Absolute paths to the files the work produced or changed, with line ranges
// wherever a range can be given, so the reviewer reads the actual lines rather
// than opening a file and hunting. A bare path is permitted by the regex only
// with a range attached; that is intentional pressure toward the reviewable
// spelling.
artifacts?: [..._citation]

})
