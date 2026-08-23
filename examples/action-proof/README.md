# action-proof (gate)

**Unit:** [08_action-proof](/Users/nsviridenko/ws/sloprail/strategy/memories/topics/20260812_no-slop/units/08_action-proof/UNIT.md)
**Nature:** gate ([decision 20260818_no-slop-primitives](/Users/nsviridenko/ws/sloprail/strategy/memories/decisions/20260818_no-slop-primitives/DECISION.md), slice 5, candidate list)

## The rule

Automation must carry PROOF of the action it took. Filling a contact form,
downloading an invoice — the proof is a screenshot of the filled form, so an
audit can later double-check every field was filled correctly. The proof is
part of the trajectory (the screenshot is a tool_use output); the rule checks
it is there and that it actually shows the action done right.

## Why gate, and why on Stop

This is the second gate example, and it shows a gate that is not a
precondition. Where `required-context-precondition` blocks a write *before* it
lands, this one wakes on **Stop** — the moment to ask "did this turn take an
auditable action, and did it prove it?" A gate is a checkpoint on an event;
here the event is the end of the turn, and what it blocks is stopping without
the required proof.

It is still a gate, not a file-guard or a mode: there is no file whose state is
guarded (the proof is a trajectory artifact, not a file on disk), and no
lifecycle to enter and exit. Just one checkpoint, one decision.

## The parts

- **`gate/screenshot-proves-fields/gate.yaml`** — `on: [{event: Stop}]`, one
  check: `prepare` + `judge`.
- **`find-action-and-proof.sh`** (`prepare`) — reads the trajectory: did a
  `fill_form`/`download_file` happen, and is there a `screenshot` output to go
  with it? Hands the judge `{action_taken, action, action_input, proof}` so
  the template never parses a transcript itself. If no action happened, says
  so — and the judge passes trivially.
- **`screenshot-shows-all-fields.md.j2`** (`judge`) — rules on whether the
  screenshot actually shows the action's fields filled with the values the
  agent supplied. Missing proof, wrong screen, or contradicted values all
  fail; it names the specific field so the verdict is auditable.

## What this shows about the gate nature

A gate's `checks` are where the **grounding / proof** primitives live — this
one grounds a claimed action in a real trajectory artifact and judges its
truth. Same `{prepare, judge}` machinery a file-guard uses, pointed at the
trajectory instead of a file. The nature needed nothing new: "proof of an
action" is a check on a Stop gate, not a fourth kind of rule.
