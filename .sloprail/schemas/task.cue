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

// THE DELIVERY EVIDENCE, REQUIRED ONLY IN `in_review`. Two lists, two kinds, and
// the split is the whole point of the review lifecycle — it is NOT the body's
// citation of the ASK.
//
// The BODY of a TASK.md cites the user's own words for what was asked, inline as
// `[quote](jsonl)` markdown links (guarded by task-body-is-human-authored). These
// two FRONTMATTER lists are the opposite end of the lifecycle: they cite what the
// agent DELIVERED, as structured citation strings, so a reviewer can weigh the
// delivery against the claim. Conflating the two — grounding a delivery claim in
// the user's ask — proves only that the work was requested, never that it was
// done. So the delivery evidence is structured frontmatter, distinct from the
// body's inline ask.
//
// Both are OPTIONAL in the schema while being MANDATORY in in_review (enforced by
// task-evidence-resolves, not the schema). CUE can express the conditional, but it
// cannot express what actually matters — that an observation's cited line is a
// real tool_result, and that an artifact's cited lines exist in the tree. Those
// are transcript and filesystem questions, and a schema sees a string. So the
// SHAPE is pinned here and the SUBSTANCE is checked in the hook.
//
// The two citation kinds have DIFFERENT PATH BASES, and that difference is
// load-bearing — it is what tells the resolver which mechanism to use:
//
//   OBSERVATION  /abs/session.jsonl:120     ABSOLUTE .jsonl transcript path,
//                                            resolved by confirming the cited LINE
//                                            is a tool_result the session produced.
//   ARTIFACT     src/foo.go:10-60           REPO-RELATIVE tree path, resolved by
//                                            confirming the file exists under the
//                                            repo and the cited lines exist.
//
// An observation's path is the transcript, opened by absolute path (a reviewer's
// working directory is nobody's business); an artifact's path is a produced file,
// named RELATIVE to the repo root so it reads the same for anyone with the tree
// checked out. This is a DELIBERATE divergence from the old schema, which demanded
// an absolute `/` for both: artifacts are repo-relative now. So the two types are
// SEPARATE definitions, not one shared citation — an observation wearing a
// repo-relative path, or an artifact wearing an absolute .jsonl, is a mis-filed
// citation the regex refuses at load time.

// _observation — an ABSOLUTE `.jsonl` transcript citation `<path>:<ranges>`.
//
// A leading `/` (absolute), then a `.jsonl`, a colon, and at least one range. A
// range is `N` or `N-M`, several comma-separated. The `.jsonl` is REQUIRED in the
// shape: an observation is proof cited into the session record, and a path that is
// not a transcript cannot carry a tool_result. task-evidence-resolves then confirms
// each cited line IS a tool_result (not the agent's prose), the substance the regex
// cannot see. Defined at the top level, outside close(): a definition is a schema
// construct, and one written inside close() would be read as a forbidden key.
_observation: =~"^/[^:]+\\.jsonl:[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$"

// _artifact — a REPO-RELATIVE tree citation `<path>:<ranges>`.
//
// NO leading `/` (`[^/:]` first byte forbids it, and forbids a bare `:` too), a
// path, a colon, and at least one range. Repo-relative so the citation is
// reviewable from any checkout; task-evidence-resolves resolves it under the repo
// root and confirms every cited line exists. An absolute path is refused here — an
// absolute path is an observation's transcript, and an absolute artifact would not
// survive a different checkout.
_artifact: =~"^[^/:][^:]*:[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$"

// PROOF THAT THE WORK HAPPENED — cited into the session transcript.
observations?: [..._observation]

// WHERE THE RESULT OF THE WORK IS — cited into the tree, repo-relative.
artifacts?: [..._artifact]

})
