package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/internal/checkstore"
)

// The watermark is not stored on its own: it is the head of the newest run the
// rule passed, at its current definition, that is still reachable. These tests
// seed the session's check results (what the Stop evaluation records) and read
// the range back through `sr-session changeset`.

const wmSession = "s-001-15"

// passRun is a rule's run at head that passed (no checks: nothing to object to).
func passRun(rule, head, ruleHash string) checkstore.CheckRun {
	return checkstore.CheckRun{CheckID: rule, BaseRef: "base", HeadRef: head,
		Metadata: map[string]any{"ruleHash": ruleHash, "eventKind": "Changeset"}}
}

// watermarkRepo is a committed rule and one commit after it, with a started
// session, returning the rule's qualified name, hash, and the commit after.
func watermarkRepo(t *testing.T) (e *Env, proj, rule, hash, c1 string) {
	t.Helper()
	e = New(t)
	proj = e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/a.md", "one\n")
	e.CommitAll(proj, "before")
	e.FileGuard(proj, "size", docsRule(""), map[string]string{"check.sh": passingCheck})
	e.CommitAll(proj, "add the rule")
	e.Run(proj, wmSession, "hello", Turns("done", Bash("b1", "true")))
	e.RemoveCheckResults(proj, wmSession) // the mock's own Stop recorded a pass; start clean

	e.WriteFile(proj, "docs/a.md", "one\ntwo\n")
	c1 = e.CommitAll(proj, "first edit")
	first, res := show(t, e, proj, e.SessionEnv(wmSession), "size")
	if res.Code != 0 || first.Origin != "floor" {
		t.Fatalf("before any run: exit %d origin %q\n%s", res.Code, first.Origin, res.Output)
	}
	return e, proj, first.Rule, first.RuleHash, c1
}

// T001_15: a run the rule passed at c1 moves the base to c1 — and only a PASS
// does: a failing run and an engine failure at a later head move nothing.
func TestT001_15_TheWatermarkIsTheNewestPassedRun(t *testing.T) {
	e, proj, rule, hash, c1 := watermarkRepo(t)

	// Refusals first: a failed run, and a run that failed as an engine, leave the
	// base where it was.
	e.WriteFile(proj, "docs/a.md", "one\ntwo\nthree\n")
	c2 := e.CommitAll(proj, "second edit")
	failed := passRun(rule, c2, hash)
	e.RecordCheckRun(proj, wmSession, failed, checkstore.CheckRecord{Subject: "changeset", Kind: "check[0]:script:./check.sh", Status: "fail"})
	broken := passRun(rule, c2, hash)
	broken.ExitCode, broken.Error = 1, "git: bad object"
	e.RecordCheckRun(proj, wmSession, broken)

	got, res := show(t, e, proj, e.SessionEnv(wmSession), "size")
	if res.Code != 0 || got.Origin != "floor" {
		t.Fatalf("a failed run moved the base: exit %d origin %q\n%s", res.Code, got.Origin, res.Output)
	}

	// Then a pass at c1: the range now starts there and holds only c2.
	e.RecordCheckRun(proj, wmSession, passRun(rule, c1, hash))
	got, res = show(t, e, proj, e.SessionEnv(wmSession), "size")
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	if got.Origin != "watermark" || got.Base != c1 || got.Head != c2 {
		t.Fatalf("range = %s %s..%s, want watermark %s..%s", got.Origin, got.Base, got.Head, c1, c2)
	}
	if n := len(got.Payload.Changeset.Commits); n != 1 || got.Payload.Changeset.Commits[0].Subject != "second edit" {
		t.Fatalf("commits = %+v, want only the one after the watermark", got.Payload.Changeset.Commits)
	}
}

// T001_16: an amend orphans the passed head. It is dropped — and reported as
// dropped — and the range widens back to the floor instead of silently shrinking.
func TestT001_16_AnAmendedAwayWatermarkIsDropped(t *testing.T) {
	e, proj, rule, hash, c1 := watermarkRepo(t)
	e.RecordCheckRun(proj, wmSession, passRun(rule, c1, hash))
	e.Git(proj, "commit", "--amend", "-m", "first edit, reworded")

	got, res := show(t, e, proj, e.SessionEnv(wmSession), "size")
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	if got.Origin != "floor" || got.DroppedWatermark != c1 {
		t.Fatalf("origin %q dropped %q, want floor with %s dropped", got.Origin, got.DroppedWatermark, c1)
	}
	if want := map[string]string{"docs/a.md": "M"}; !equal(filesOf(got), want) {
		t.Fatalf("files = %v, want the whole range again: %v", filesOf(got), want)
	}
}

// T001_17: a pass is not keyed on the rule's definition: work up to it was approved,
// even under an older rule. Edit the rule and the hash changes (so verdicts are not
// replayed), but the watermark stays, and only what comes after the pass is judged.
func TestT001_17_AnEditedRuleKeepsItsWatermark(t *testing.T) {
	e, proj, rule, hash, c1 := watermarkRepo(t)
	e.RecordCheckRun(proj, wmSession, passRun(rule, c1, hash))
	e.WriteFile(proj, ".sloprail/file-guard/size/check.sh", passingCheck+"# edited\n")

	got, res := show(t, e, proj, e.SessionEnv(wmSession), "size")
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	if got.RuleHash == hash {
		t.Fatalf("the edit left the rule's hash unchanged")
	}
	if got.Origin != "watermark" || got.Base != c1 {
		t.Fatalf("origin %q base %s; the pass reached under the older rule must still be the watermark (%s)", got.Origin, got.Base, c1)
	}
}

// T001_18: another rule's pass is not this rule's.
func TestT001_18_APassIsPerRule(t *testing.T) {
	e, proj, _, hash, c1 := watermarkRepo(t)
	e.RecordCheckRun(proj, wmSession, passRun("file-guard/other", c1, hash))

	got, res := show(t, e, proj, e.SessionEnv(wmSession), "size")
	if res.Code != 0 || got.Origin != "floor" {
		t.Fatalf("exit %d origin %q:\n%s", res.Code, got.Origin, res.Output)
	}
}

// T001_19: a rule's identity is its whole .sloprail root, not its folder alone. A
// shared lib edited OUTSIDE the rule's own folder changes the hash, and commits made
// after a pass are judged by the new rule — but the pass stays the watermark, so
// only the commits after it are judged: the work approved before is not judged again.
func TestT001_19_AnEditOutsideTheRuleFolderButInsideSloprailChangesTheHashNotTheWatermark(t *testing.T) {
	e, proj, rule, hash, c1 := watermarkRepo(t)
	e.RecordCheckRun(proj, wmSession, passRun(rule, c1, hash))

	e.WriteFile(proj, ".sloprail/lib/shared.sh", "#!/bin/sh\n# what rules share\n")
	e.CommitAll(proj, "edit the shared lib")
	e.WriteFile(proj, "docs/a.md", "one\ntwo\nthree\n")
	head := e.CommitAll(proj, "third edit")

	got, res := show(t, e, proj, e.SessionEnv(wmSession), "size")
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	if got.RuleHash == hash {
		t.Fatalf("editing .sloprail/lib/shared.sh left the rule's hash unchanged")
	}
	if got.Origin != "watermark" || got.Base != c1 || got.Head != head {
		t.Fatalf("range = %s %s..%s, want watermark %s..%s", got.Origin, got.Base, got.Head, c1, head)
	}
	files := got.Payload.Changeset.Files
	if len(files) != 1 || files[0].Path != "docs/a.md" || files[0].OldContent != "one\ntwo\n" || files[0].NewContent != "one\ntwo\nthree\n" {
		t.Fatalf("files = %+v, want only the change made after the pass", files)
	}
	if n := len(got.Payload.Changeset.Commits); n != 2 {
		t.Fatalf("commits = %+v, want the lib edit and the third edit", got.Payload.Changeset.Commits)
	}
}
