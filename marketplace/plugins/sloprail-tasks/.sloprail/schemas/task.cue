// The frontmatter every memories/tasks/<name>/TASK.md must carry.
//
// Checked by task-evidence-resolves via `sr-file validate`, which reads the
// frontmatter of a .md and vets it as a CUE document. Also read by
// no-unfinished-work-at-turn-end (`sr-file validate --emit | jq -r .status`).
//
// This schema is SHIPPED BY THE PLUGIN but read from the CONSUMER's project at
// $SR_WORKSPACE/.sloprail/schemas/task.cue — installing the plugin means placing
// this file there, the same as any project schema. The scripts resolve it under
// $SR_WORKSPACE, never under the plugin, because a task lives in the consumer's
// tree.
//
// CLOSED — and closed EXPLICITLY, with the `close()` below, because CUE's
// default is open and a comment claiming otherwise is not a constraint. This
// was measured: before the definition was wrapped, a task carrying a `sneaky:`
// key it had invented validated cleanly.

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
priority!: "P0" | "P1" | "P2" | "P3"

// THE EVIDENCE, REQUIRED ONLY IN `in_review`.
//
// Both are lists of `<path>:<ranges>` citations, and both are OPTIONAL here
// while being MANDATORY in in_review (enforced by task-evidence-resolves, not
// the schema). That split is deliberate: CUE can express the conditional, but
// it cannot express what actually matters — that the path resolves, that the
// line range exists, that the cited JSONL line is a real record rather than the
// agent's own prose. Those are filesystem questions, and a schema sees a string.
//
// So the shape is pinned here and the substance is checked in the hook. The
// regex demands a leading `/` — an absolute path — then a colon, then at least
// one range. A range is `N` or `N-M`, several comma-separated.
// Defined at the top level, outside the closed struct: a definition is a schema
// construct rather than a field, and one written inside close() would be read as
// a key the document is not allowed to carry.
_citation: =~"^/[^:]+:[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$"

// PROOF THAT THE WORK HAPPENED — cited into the session record.
observations?: [..._citation]

// WHERE THE RESULT OF THE WORK IS — cited into the tree.
artifacts?: [..._citation]

})
