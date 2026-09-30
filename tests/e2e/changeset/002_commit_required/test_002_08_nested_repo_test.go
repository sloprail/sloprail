package e2e

import (
	"testing"
)

// T002_08: an untracked nested repository — a clone the agent made to look at —
// is not this repository's work to commit, so it is never "uncommitted guarded
// work", even under a rule that selects everything but dot-files (`path != ""`; the mock keeps its own
// scenario file in the tree). Its contents
// never reach a rule; telling the agent to commit it as a gitlink would be wrong.
func TestT002_08_AnUntrackedNestedRepositoryIsNotOwedACommit(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "everything", "match: 'path != \"\" and not (path startsWith \".\")'\nchecks:\n  - script: ./check.sh\n", map[string]string{"check.sh": passing})
	e.CommitAll(proj, "the project and its rule")

	e.Run(proj, "s-002-08", "look at a reference clone", Turns("done",
		Bash("b1", "mkdir -p vendor/clone && cd vendor/clone && git init -q && echo x > f.txt && git add . && git -c user.email=t@e.invalid -c user.name=T commit -qm inner"),
	))
	if errs := commitRequired(e.BlockingErrorsFrom(proj, "s-002-08", "Stop")); len(errs) != 0 {
		t.Fatalf("an untracked nested repository was treated as uncommitted guarded work: %q", errs)
	}

	// The control: a plain untracked file beside it IS owed a commit under this rule.
	e.Run(proj, "s-002-08", "and a note", Turns("done", Write("w1", "vendor/note.md", "note\n")))
	if errs := commitRequired(e.BlockingErrorsFrom(proj, "s-002-08", "Stop")); len(errs) == 0 {
		t.Fatal("premise: the rule selects everything, so an ordinary new file beside the clone must be refused")
	}
}
