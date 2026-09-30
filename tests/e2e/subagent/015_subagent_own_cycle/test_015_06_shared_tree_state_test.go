package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T015_06: in ONE tree, a sub-agent's state and its parent's are still separate.
//
// This is the test that makes the state invariant mean something, and the
// isolated-worktree tests (T015_03, T015_04) cannot replace it. MEASURED: with
// the store keyed on <workspace>/<session>, mutating the SESSION half to a
// constant — pooling every session in a tree into one database — leaves both of
// those green. They pass because the two sub-agents are in different WORKTREES,
// so the workspace half of the key separates them no matter what the session
// half does. The tree boundary was doing the work the session key was being
// credited with.
//
// A shared-tree dispatch removes that. One cwd, one workspace, one database
// directory — so the ONLY thing that can keep the parent's notes and the
// sub-agent's apart is the session key itself. That is precisely the invariant:
//
//	"The state a sub-agent's guardrails write is scoped to that sub-agent, and
//	 never pools with the state of the session that spawned it."
//
// Both directions are checked, and both are the real failure the spec names:
//
//   - the sub-agent must not read the parent's notes — otherwise a rule that
//     remembers something remembers it about work it was never watching;
//   - the parent must not read the sub-agent's — otherwise a sub-agent's note
//     that it had already refused exempts the parent from a refusal the parent
//     never received.
//
// The positive control is built in and is what stops the whole thing being
// vacuous: each session must be shown to read back its OWN earlier note. Two
// sessions that both store nothing also fail to read each other's, and would
// satisfy every isolation assertion here while proving the opposite of what is
// claimed.
func TestT015_06_InOneTreeASubagentsStateIsStillItsOwn(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "memo", readsBackItsOwnState, map[string]string{"record.sh": readsBackScript})
	e.GitInit(proj)

	// The sub-agent writes into the SAME tree the root is working in. Bash
	// rather than Write, because the mock applies Bash and it is the landed file
	// that gives the sub-agent's cycle something to judge.
	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo 'the sub-agents own work' > written-by-the-sub.md"),
	))

	res := e.Run(proj, "s-015-06", "delegate into the same tree", Turns("root done",
		Bash("rb1", "echo root > root-before.md"),
		Dispatch("d1", "do it here", sub, ""),
		Bash("rb2", "echo root > root-after.md"),
	))

	if !res.Saw("root done") {
		t.Fatalf("the shared-tree delegated cycle did not complete:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("the sub-agent hit the retry cap:\n%s", res.Output)
	}

	// One tree, genuinely: no worktree was bound. With a worktree the workspace
	// half of the store key would separate the two sessions on its own, and this
	// test would be T015_03 under another name — passing against a store that
	// pools every session in a tree.
	if trees := worktrees(t, proj); len(trees) != 0 {
		t.Fatalf("a worktree was bound (%v) for a dispatch that asked for none. This test is "+
			"about two sessions sharing ONE workspace, where only the session key can keep their "+
			"state apart", trees)
	}

	// Both cycles wrote into the one project tree, so both ledgers are here.
	lines := e.FileGuardLedgerLines(proj, "memo", "log")
	if len(lines) == 0 {
		t.Fatalf("nothing was judged at all:\n%s", res.Output)
	}

	// Split the ledger by the identity each line was judged under. Two sessions
	// ended over this tree — the sub-agent's cycle and the root's — so there must
	// be two.
	bySession := map[string][]string{}
	var order []string
	for _, l := range lines {
		id := sessionOf(l)
		if _, seen := bySession[id]; !seen {
			order = append(order, id)
		}
		bySession[id] = append(bySession[id], l)
	}
	if len(order) < 2 {
		t.Fatalf("only one session (%v) judged anything in this tree. The sub-agent's cycle and "+
			"the root's are different sessions; one identity here means the sub-agent's work was "+
			"judged as the dispatching session's own. Ledger: %v", order, lines)
	}

	// Which session is the sub-agent's: the one that judged the sub-agent's file
	// FIRST, at its own cycle. The root judges it too, later, at its own Stop —
	// see T015_02, where that double judgement is the pinned behaviour.
	var subID string
	for _, l := range lines {
		if pathOf(l) == "written-by-the-sub.md" {
			subID = sessionOf(l)
			break
		}
	}
	if subID == "" {
		t.Fatalf("the sub-agent's own file was judged by nobody, so there is no sub-agent cycle "+
			"here to have state of its own. Ledger: %v", lines)
	}

	// THE POSITIVE CONTROL, per session. Each of the two must read back a note
	// one of its OWN earlier hooks left. Without this, "neither read the other's"
	// is equally true of two stores that never worked.
	for _, id := range order {
		if !carriedWithin(bySession[id]) {
			t.Fatalf("session %s never read back anything an earlier hook of its own had stored. "+
				"Its store is not working, so the isolation assertions below would pass against an "+
				"engine that remembers nothing at all. Its lines: %v", id, bySession[id])
		}
	}

	// THE CLAIM, and it needs stating carefully, because the obvious phrasing is
	// wrong and measuring it is what showed that.
	//
	// It is NOT "the sub-agent never sees a file the root wrote". A shared-tree
	// sub-agent's cycle diffs the whole tree, and the root's own files are part
	// of that tree, so its cycle legitimately judges root-before.md too and
	// chains its own note over it. MEASURED: the sub-agent's cycle reads back
	// `root-before.md` — written into the SUB-AGENT'S scope, by the sub-agent's
	// own earlier hook, one line above in its own ledger. Asserting that away
	// would be asserting against correct behaviour, and the first version of
	// this test did exactly that and failed on a clean engine.
	//
	// The invariant is about WHOSE STORE a note lands in, not about which files
	// a cycle sees. So what is checked is that every value a session reads back
	// was written BY THAT SAME SESSION — no line may read back a note whose only
	// writer was a different session. That is precisely "never pools", and it is
	// false the moment two sessions share one database.
	for _, id := range order {
		writtenHere := map[string]bool{}
		for _, l := range bySession[id] {
			got := beforeOf(l)
			if got != "" && !writtenHere[got] {
				// Read back something this session never wrote. The only way a
				// value reaches this scope without this session having put it
				// there is another session's hook writing into the same store.
				var writers []string
				for _, other := range order {
					if other == id {
						continue
					}
					for _, ol := range bySession[other] {
						if pathOf(ol) == got {
							writers = append(writers, other)
						}
					}
				}
				t.Fatalf("session %s read back the note %q, which it never wrote — it was written "+
					"by %v. Two sessions are sharing one store: a sub-agent's note that it had "+
					"already refused would exempt the parent from a refusal the parent never "+
					"received, and a session spawning ten sub-agents would have all ten reading "+
					"each other's notes as their own.\n  reading line: %s\n  that session's ledger: %v",
					id, got, writers, l, bySession[id])
			}
			writtenHere[pathOf(l)] = true
		}
	}

	// And the sub-agent's own cycle really did record something of its own,
	// or the loop above ran over a session that stored nothing and concluded
	// nothing.
	if !containsPath(bySession[subID], "written-by-the-sub.md") {
		t.Fatalf("the sub-agent's session judged none of its own work (%v)", bySession[subID])
	}
}

// carriedWithin reports whether some line in one session's ledger read back a
// value an EARLIER line of that same session had written.
//
// The positive control for a session's own store, expressed without pinning a
// particular predecessor: which file a given hook sees before it depends on the
// order the engine dispatches a cycle's Post events in, which nothing promises
// and which this test has no business fixing. What cannot happen by accident is
// the property itself — a value written by one hook process and read back by a
// later one in the same scope.
func carriedWithin(lines []string) bool {
	written := map[string]bool{}
	for _, l := range lines {
		if got := beforeOf(l); got != "" && written[got] {
			return true
		}
		written[pathOf(l)] = true
	}
	return false
}
