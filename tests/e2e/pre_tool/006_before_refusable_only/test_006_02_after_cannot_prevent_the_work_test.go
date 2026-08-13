// before_refusable_only: refusing a `before` event prevents the work, while
// refusing an `after` event demands the work be corrected.
//
// This file covers the `after` half. It was gated behind the
// sloprail_post_dispatch build tag until impl/stop-diff-impl merged (7289bc3);
// the tag's own header said to delete it on that merge, and the prerequisites it
// named — Env.Exists and Env.BlockingErrors — are both present on the harness
// now. Ungated during the invariant audit, with each assertion re-confirmed to
// fail against a deliberately broken engine (blanking the objections check in
// runPostDispatch turns T006_02 red).

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The `after` half of before_refusable_only, restored through the plugin.
//
// # WHY THIS FILE EXISTS AGAIN
//
// It had a test once, and that test hand-wrote a PostToolUse hook into the
// project's settings.json — wiring no user has. What it proved was a property of
// an arrangement the harness had built for itself. It was deleted rather than
// repaired because the harness could no longer express it at all: writeSettings
// emits the plugin and nothing else, and `sloprail session stop` dispatched
// nothing, so a guardrail bound to a Post kind loaded without complaint, the
// write landed, and the hook never ran.
//
// Both halves of that reason have expired. The end of a cycle now diffs the tree
// and dispatches PostFileCreate/Update/Delete and TurnEnd, and the plugin
// already registers Stop — so the invariant is expressible through the product's
// own binding, which is what should have carried it in the first place. Nothing
// here touches settings.json.
//
// # WHAT "CANNOT PREVENT THE WORK" MEANS, PRECISELY
//
// It has a narrower meaning than the deleted test assumed, and the difference is
// the whole point of this file. A Post refusal:
//
//   - CANNOT undo the write. The file is on disk and the cycle is over. An
//     engine claiming otherwise would be promising a rollback it never performs.
//     That is the `after` half of before_refusable_only, and it is what
//     separates the two timings: `before` prevents the ACTION, `after` cannot.
//
//   - MUST still stop the TURN. That is not a contradiction of the above, it is
//     the mechanism by which an after-the-fact rule gets anything corrected. A
//     refusal that let the turn end would be a complaint nobody acts on.
//
// Conflating those two was a real design error in this project before the owner
// corrected it. So this test asserts BOTH, and asserts them on different
// channels, because a test checking only the file could not tell a blocking
// engine from a silently-permitting one: the file survives either way. That is
// exactly the trap that makes a test's name match a claim its data never
// reaches.
//
// # WHERE THE ANSWERS ARE READ FROM
//
// Not from the stream, for the block. Measured through this harness: a Stop
// refusal never appears in the mock's stdout at all. It arrives as a
// hook_blocking_error attachment inside the conversation's own record, which is
// what the agent reads on its next turn. Result.Refused() scans the stream for
// the PreToolUse markers and would answer "permitted" for every Post refusal
// ever made — so using it here would produce a test that cannot fail.
//
// The block itself is read off the mock being driven round again: a blocked stop
// makes the agent continue past its own end, so more than one final result line
// is emitted. One result line means the turn simply ended.

// refuseAfterTheWriteLanded binds the refusing script to the after-the-fact
// point rather than the before-the-fact one. The only difference from
// refuseEveryWrite in T006_01 is the kind — same script, same rule, opposite
// timing, which is what makes the pair a comparison rather than two unrelated
// tests.
const refuseAfterTheWriteLanded = `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh
---

# Objects to the write after it has already landed
`

// T006_02: a refusal AFTER the fact cannot prevent work that already landed —
// but it does stop the turn.
//
// The mirror of T006_01. Same write, same refusing hook, bound one timing later;
// T006_01 asserts the file is absent, this asserts it is present. Between them
// they are the whole of before_refusable_only, and neither means much without
// the other: a guardrail that refused nothing would pass this one alone.
func TestT006_02_AnAfterRefusalCannotPreventTheWork(t *testing.T) {
	e := New(t)
	proj := e.Project()

	// The tree needs a baseline to be compared against, and the baseline is a
	// commit. Without git there is no point to measure from, no Post events are
	// produced at all, and every assertion below would be reading the silence of
	// a cycle that never dispatched rather than the behaviour of one that did.
	e.GitInit(proj)

	// The hook records that it ran into a directory OUTSIDE the project.
	//
	// A hook's working directory is its guardrail's own folder, which sits
	// inside the tree the engine compares — so a hook logging there creates an
	// untracked file, which is itself a change this same cycle reports and this
	// same rule then refuses. One rule objecting to one file quietly becomes
	// two, and the run stops being the single-refusal case this test means to
	// describe.
	ranLog := filepath.Join(t.TempDir(), "ran.log")
	e.Guardrail(proj, "afterguard", refuseAfterTheWriteLanded, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> " + ranLog +
			"\necho 'refused after it had already landed' >&2\nexit 1\n",
	})

	// Committed before the session, so the guardrail's own files are part of the
	// baseline rather than part of what the cycle appears to have changed.
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")

	got := e.Run(proj, "s-006-02", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	// The hook RAN. This is the assertion the whole file rests on, and the one
	// whose absence made the original deletion correct: a guardrail bound to a
	// kind nothing dispatches is silent, and a silent hook is indistinguishable
	// from a hook that ran and permitted. Without this, every assertion below
	// would pass against an engine that dispatches nothing at all — the file
	// would exist because nobody objected, not because an objection could not
	// undo it.
	if _, err := os.Stat(ranLog); err != nil {
		t.Fatalf("the Post hook never ran, so nothing below is evidence about after-the-fact refusals:\n%s", got.Output)
	}

	// The work LANDED and stayed landed. The `after` half of the invariant: a
	// refusal at an after-the-fact point demands a correction, it does not
	// perform one. If this file were gone the engine would have rolled back a
	// write it had already allowed.
	if !e.Exists(proj, filepath.Join("some", "notes.md")) {
		t.Errorf("a hook refusing AFTER the write removed the file — an after-the-fact refusal must demand a correction, not perform one")
	}

	// The turn was BLOCKED. Separate from the file, on a separate channel, and
	// the reason this test can fail: the file survives whether the engine blocks
	// or silently permits, so without this the test would be green against an
	// engine that let the turn end with the violation unaddressed.
	//
	// A blocked stop drives the mock round again, so it emits its final result
	// more than once. One result line means the turn simply ended.
	if n := strings.Count(got.Output, `"subtype":"success"`); n < 2 {
		t.Errorf("the turn ended despite the guardrail refusing (%d result lines) — an after-the-fact refusal cannot undo the write, but it must stop the turn, which is the only way it gets anything corrected:\n%s", n, got.Output)
	}

	// And the agent was TOLD why. Of the channels that block a Stop, several
	// deliver no text at all — an engine using one of those leaves the agent
	// stopped with nothing to act on, which is worse than permitting the work.
	//
	// Read from the blocking attachments, not from the record as a whole: a
	// guardrail's own folder path travels on every hook payload, so searching
	// the file for the rule's name finds it whether or not the refusal ever
	// named it. That is an assertion that cannot fail, and this suite has
	// shipped that mistake before.
	blocking := e.BlockingErrors(proj, "s-006-02")
	if len(blocking) == 0 {
		t.Fatalf("the turn was blocked but no reason reached the agent — it is stopped with nothing to act on")
	}
	told := strings.Join(blocking, "\n")
	if !strings.Contains(told, "refused after it had already landed") {
		t.Errorf("the hook's own words did not reach the agent, so it cannot know what to fix:\n%s", told)
	}
	if !strings.Contains(told, "afterguard") {
		t.Errorf("the refusal did not name the guardrail that produced it:\n%s", told)
	}
}
