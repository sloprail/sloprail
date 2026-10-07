package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A range is tracked in a folder only when it is inside a git repository AND at least one
// file-guard loads for it. A folder with none is still a session folder (its gates apply) but
// nothing is tracked there, and Stop says nothing about it.

func bare(t *testing.T) (*Env, string) {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.NoAutoCheck())
	proj := e.Project()
	e.GitInit(proj)
	return e, proj
}

// T056_07: a git repository with no file-guard has no tracked range, and a folder that is not a
// repository at all is not tracked either.
func TestT056_07_NoFileGuardNoRange(t *testing.T) {
	e, proj := bare(t)
	const sess = "s-056-07"
	notRepo := t.TempDir()

	e.Run(proj, sess, "work", Turns("done",
		Bash("b1", "git commit -q --allow-empty -m x"),
		Bash("b2", "cd "+notRepo+" && touch a.txt"),
	))

	if rs := ranges(t, e, proj, sess); len(rs) != 0 {
		t.Fatalf("a folder with no file-guard has a tracked range: %+v", rs)
	}
	if got := e.BlockingErrorsFrom(proj, sess, "Stop"); len(got) != 0 {
		t.Fatalf("Stop said something about a folder with nothing to verify: %v", got)
	}
}

// T056_08: a repository the session works in outside its own tree is a session folder either
// way, but is tracked only if its own rules include a file-guard.
func TestT056_08_AnotherRepositoryIsTrackedOnlyWhenItHasAFileGuard(t *testing.T) {
	e, proj := bare(t)
	const sess = "s-056-08"
	plain := e.Project()
	e.GitInit(plain)
	guarded := e.Project()
	e.GitInit(guarded)
	e.FileGuard(guarded, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	e.CommitAll(guarded, "the rule")

	e.Run(proj, sess, "work elsewhere", Turns("done",
		Bash("b1", "git -C "+plain+" commit -q --allow-empty -m a"),
		Bash("b2", "git -C "+guarded+" commit -q --allow-empty -m b"),
	))

	var folders []string
	for _, f := range e.SessionFolders(proj, sess) {
		folders = append(folders, f.Path)
	}
	if len(folders) < 3 {
		t.Fatalf("the other repositories are not registered as session folders: %v", folders)
	}
	rs := ranges(t, e, proj, sess)
	if len(rs) != 1 || rs[0].Folder != real(t, guarded) {
		t.Fatalf("only the repository with a file-guard should be tracked, got %+v", rs)
	}
}

// T056_09: a rule added mid-session starts the tracking at the next hook.
func TestT056_09_RulesAddedMidSessionStartTheTracking(t *testing.T) {
	e, proj := bare(t)
	const sess = "s-056-09"

	e.Run(proj, sess, "start", Turns("done", Bash("b1", "true")))
	if rs := ranges(t, e, proj, sess); len(rs) != 0 {
		t.Fatalf("tracked before any file-guard existed: %+v", rs)
	}

	e.FileGuard(proj, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	e.CommitAll(proj, "add a rule")
	e.Run(proj, sess, "continue", Turns("done", Bash("b2", "true")))

	if rs := ranges(t, e, proj, sess); len(rs) != 1 || rs[0].Head != "main" {
		t.Fatalf("the range was not tracked once the file-guard appeared: %+v", rs)
	}
}
