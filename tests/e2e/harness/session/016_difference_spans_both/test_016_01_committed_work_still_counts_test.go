package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness/session/changesetkit"
)

// difference_spans_both: a cycle's difference covers work that has been
// committed and work that has not.
//
// The spec's reasoning: "An agent that commits during a cycle leaves a tree with
// nothing outstanding in it. A difference that only looked at what is
// outstanding would find nothing and report that the cycle changed nothing,
// which is precisely wrong."
//
// The failing implementation this guards against is the obvious one: `git
// status`, or a diff against HEAD. Both are empty immediately after a commit.
// The correct comparison is against the session's recorded point, which does not
// move when the agent commits — established on impl/baseline-mark's T005_03.

// recordEverything is a NEW-FORMAT file-guard that records every Changeset it is handed and permits unconditionally. `match: "**/*.md"` selects
// every markdown file at any depth — a single file-guard that fires on every committed change. The ledger (`seen`, no `.md`)
// is not matched, so the guard cannot re-observe its own bookkeeping.
const recordEverything = `match: "**/*.md"
checks:
  - script: ./record.sh
`

// T016_01: work the agent committed during the cycle is still reported.
//
// The whole cycle's output is committed, so the tree has nothing outstanding at
// the moment the difference is taken. `git status` is empty; a diff against HEAD
// is empty. Only a comparison against the session's own starting point finds
// this file — and it must, because committing is not a way to escape review.
func TestT016_01_CommittedWorkIsStillReported(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("seen")
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": led.RecordScript()})
	e.CommitAll(proj, "the guardrail before the session")

	e.Run(proj, "s-016-01", "write and commit", Turns("done",
		Write("w1", "committed-work.md", "written then committed\n"),
	).ThenCommit("agent commit"))

	// The premise: the agent's work really was committed, so an engine looking
	// only at outstanding work genuinely has nothing to find. Without this check
	// a failure to commit would make the test pass for the wrong reason.
	//
	// Asked of the file rather than of `git status --porcelain` being empty. The
	// guardrail writes its own ledger inside the project, and that file is
	// untracked, so the tree is never wholly clean during a run — an emptiness
	// check here fails for a reason that has nothing to do with the invariant.
	// What matters is that THIS path is committed and not outstanding.
	if status := e.Git(proj, "status", "--porcelain", "--", "committed-work.md"); status != "" {
		t.Fatalf("the agent's file is still outstanding (%q), so this does not test the "+
			"committed case at all", status)
	}
	if e.Git(proj, "log", "--oneline", "--", "committed-work.md") == "" {
		t.Fatalf("the agent's file was never committed, so this does not test the committed case")
	}

	got := changesetkit.Files(t, led.Lines())
	if !changesetkit.Saw(got, "committed-work.md") {
		t.Fatalf("work the agent committed mid-cycle was not reported: %v — the tree is clean, "+
			"so a difference that only looked at outstanding work found nothing and called the "+
			"cycle empty; committing must not be a way out of review", got)
	}
}

// T016_02: outstanding work is refused until committed, and both halves then arrive.
//
// On the commit model the tree's uncommitted work is not judged: the Stop is
// refused ("commit your work") until the agent commits it, so a cycle can no longer
// end with the two kinds of work side by side. The intent of "spans both" is kept:
// the one committed mid-cycle and the one committed only after the refusal end up in
// the same changeset, and neither is lost.
func TestT016_02_CommittedAndUncommittedWorkBothArrive(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("seen")
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": led.RecordScript()})
	e.CommitAll(proj, "the guardrail before the session")

	const sess = "s-016-02"
	e.Run(proj, sess, "commit one, leave one", Turns("done",
		Write("w1", "committed.md", "this one is committed\n"),
		harness.CommitPaths("b1", "agent commit", "committed.md"),
		Write("w2", "outstanding.md", "this one is not\n"),
	))

	// The premise: one is committed, the other is not, and the Stop refused
	// for the outstanding one without judging it.
	if status := e.Git(proj, "status", "--porcelain", "--", "committed.md"); status != "" {
		t.Fatalf("the file meant to be committed is still outstanding (%q)", status)
	}
	e.AssertCommitRequired(proj, sess, "outstanding.md")
	if got := changesetkit.Files(t, led.Lines()); changesetkit.Saw(got, "outstanding.md") {
		t.Fatalf("uncommitted work was judged before it was committed: %v", got)
	}
	seen := len(CommitRequired(e.BlockingErrorsFrom(proj, sess, "Stop")))

	e.Run(proj, sess, "now commit it", Turns("committed").ThenCommit("the outstanding file"))
	e.NoCommitRequired(proj, sess, seen)

	got := changesetkit.Files(t, led.Lines())
	if !changesetkit.Saw(got, "committed.md") {
		t.Fatalf("the committed half of the cycle's work is missing: %v", got)
	}
	if !changesetkit.Saw(got, "outstanding.md") {
		t.Fatalf("the half committed after the refusal is missing: %v", got)
	}
}
