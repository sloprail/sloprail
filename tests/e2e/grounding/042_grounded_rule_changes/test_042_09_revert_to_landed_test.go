package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// landedHistory puts a later edit of the demo rule's script on the default branch before
// the session begins (the rule's content went demoScript -> demoLoosened), with the given
// commit trailers, so the session can undo it.
func landedHistory(t *testing.T, e *harness.Env, proj string, trailers ...string) {
	t.Helper()
	e.WriteFile(proj, ".sloprail/file-guard/demo/check.sh", demoLoosened)
	e.CommitAll(proj, "loosen the demo rule", trailers...)
	e.Git(proj, "push", "-q", "origin", "HEAD:refs/heads/"+e.Git(proj, "branch", "--show-current"))
	e.Git(proj, "fetch", "-q", "origin")
}

// T042_09: restoring a rule file to the version the default branch had, when everything
// that changed it since carried no citation, undoes only uncited changes: no citation
// needed (the judge is a failing stub, so it is not asked either).
func TestT042_09_RevertOfUncitedEditsNeedsNoCitation(t *testing.T) {
	e := New(t)
	proj := project(t, e)
	landedHistory(t, e, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR042 the judge ran on a revert"}`)
	e.Run(proj, "s-042-09", "put the demo rule back the way the architecture PR landed it", Turns("done",
		editScript(t, "w1", ".sloprail/file-guard/demo/check.sh", demoScript)...,
	).ThenCommit("revert the uncited edit of the demo rule"))
	if got, out := blocked(e, proj, "s-042-09"); got {
		t.Fatalf("a revert to the landed version after uncited edits was refused:\n%s", out)
	}
}

// T042_10: undoing a CITED change (the user approved it) still needs the user's words.
func TestT042_10_RevertOfACitedChangeStillNeedsGrounding(t *testing.T) {
	e := NewUncited(t)
	proj := project(t, e)
	landedHistory(t, e, proj, harness.CitesUser("loosen the demo rule"))
	refuseThenPass(t, e, proj, "s-042-10", "put the demo rule back",
		Turns("done", editScript(t, "w1", ".sloprail/file-guard/demo/check.sh", demoScript)...),
		harness.CitesUser("put the demo rule back"), advice...)
}

// T042_11: content the default branch never had is no revert, however uncited its history.
func TestT042_11_ArbitraryContentStillNeedsGrounding(t *testing.T) {
	e := NewUncited(t)
	proj := project(t, e)
	landedHistory(t, e, proj)
	refuseThenPass(t, e, proj, "s-042-11", "tweak the demo rule",
		Turns("done", editScript(t, "w1", ".sloprail/file-guard/demo/check.sh", demoLoosened+"# and something new\n")...),
		harness.CitesUser("tweak the demo rule"), advice...)
}
