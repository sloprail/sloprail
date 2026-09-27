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
//   in_review   — the agent claims it is finished: the write that set it cited
//                 the tool output proving the work, and `artifacts` names the
//                 result. NOT the agent's verdict: the claim is what this status
//                 records, and the review guardrail is what judges it.
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

// THE DELIVERY EVIDENCE. A task file holds DERIVED TEXT ONLY — nothing that names
// a session transcript, because a transcript path resolves on no other machine.
// So of the three things a task's lifecycle is grounded in, only one lives here:
//
//   the ASK                the user's own words for what was asked. Cited on the
//                          WRITE that creates the task or changes its body —
//                          `sr-file write|edit … --cite:user '<exact words>'` —
//                          and checked by task-body-is-human-authored against the
//                          event's citations. The body states the ask; it carries
//                          no link.
//   PROOF IT HAPPENED      tool output (a test run that came back green). Cited on
//                          the WRITE that moves the task into in_review —
//                          `sr-file edit … --cite:tool_result '<exact output>'` —
//                          and checked by task-evidence-resolves, then weighed by
//                          task-review, which is handed the full tool result.
//   WHERE THE RESULT IS    the produced files, at the lines that changed:
//                          `artifacts` below. Repository content, so it CAN live
//                          in the file, and reads the same on every checkout.
//
// Keeping the ask and the proof apart is the point of the review lifecycle:
// grounding a delivery claim in the user's ask proves only that the work was
// requested, never that it was done.
//
// `artifacts` is OPTIONAL in the schema while MANDATORY in in_review (enforced by
// task-evidence-resolves, not the schema). CUE can express the conditional, but it
// cannot express what actually matters — that the cited lines exist in the tree.
// That is a filesystem question, and a schema sees a string. So the SHAPE is
// pinned here and the SUBSTANCE is checked in the hook.

// _artifact — a REPO-RELATIVE tree citation `<path>:<ranges>`.
//
// NO leading `/` (`[^/:]` first byte forbids it, and forbids a bare `:` too), a
// path, a colon, and at least one range. A range is `N` or `N-M`, several
// comma-separated. Repo-relative so the citation is reviewable from any checkout;
// task-evidence-resolves resolves it under the repo root and confirms every cited
// line exists. An absolute path is refused here — it would not survive a
// different checkout. Defined at the top level, outside close(): a definition is
// a schema construct, and one written inside close() would be read as a
// forbidden key.
_artifact: =~"^[^/:][^:]*:[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$"

// WHERE THE RESULT OF THE WORK IS — cited into the tree, repo-relative.
artifacts?: [..._artifact]

// DEPENDS_ON — other tasks that must be DONE (deleted, per the lifecycle above)
// before this one may leave backlog/blocked for to_do/in_progress/in_review.
// Checked by task-dependencies-resolve, not this schema: CUE can pin the SHAPE
// of an id but not whether it names a real task, nor whether the graph it
// forms is acyclic — those are filesystem questions.
// `<group>/<task-name>` matches the two path segments between memories/tasks/
// and /TASK.md, e.g. "auth/migrate-tokens" for
// memories/tasks/auth/migrate-tokens/TASK.md.
//
// A depends_on ID MUST NEVER OUTLIVE THE TASK IT NAMES. When a task is
// deleted (the reviewer approving it), every OTHER task's depends_on
// referencing it must be stripped in that SAME change —
// no-dangling-dependencies-at-turn-end (a Stop gate) refuses to let a turn
// end while any depends_on entry names a folder that does not exist. This is
// why task-dependencies-resolve itself does not try to distinguish "this id
// used to be a real, now-finished task" from "this id was never real" (a
// typo): under the no-dangling invariant, an id that resolves to nothing is
// ALWAYS wrong to be sitting in depends_on, whichever of the two it is — the
// fix is the same either way, strip it (or, for a genuine mistake, restore
// the task).
_dep_id: =~"^[^/]+/[^/]+$"
depends_on?: [..._dep_id]

// THERE IS NO ready_when FIELD. A task's start conditions are FILES, not
// frontmatter — memories/tasks/<group>/<name>/gates/<gate-name>.md (a
// judgment condition, a prompt) or gates/<gate-name>.sh (a deterministic
// condition, a script; exit 0 passes). See task-gates-hold (which enforces
// every gate before backlog/blocked -> to_do/in_progress) and
// task-gate-is-grounded (which judges a gate file against the task's stated
// ask on write, the protection against weakening or fabricating one). Kept
// as files rather than a frontmatter list because a gates/ directory is
// itself the file-guard system's native unit — a `.sh` gate is TESTED by
// literally running it, not by a script re-interpreting a string a task
// author wrote, and a `.md` gate IS the judge prompt, not a description of
// one. Frontmatter still carries depends_on (a list of other tasks' ids —
// there is no "file per dependency" shape that says anything more than the
// id already does).

})
