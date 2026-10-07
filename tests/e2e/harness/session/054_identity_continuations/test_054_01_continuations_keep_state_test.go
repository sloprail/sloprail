package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// probeScript is the harness's store probe (harness.ControlScript) logging to a
// ledger OUTSIDE the repository: in the rule's own folder the log would be committed
// with the agent's work, and a rule whose folder changed forgets its earlier passes,
// so the next cycle's range would collapse to nothing and the probe would not run.
func probeScript(ledger string) string {
	return "#!/bin/sh\ncat >/dev/null\n" +
		"echo \"before=[$(sr-session state get seen 2>&1)]\" >> '" + ledger + "'\n" +
		"sr-session state set seen yes >/dev/null 2>&1\nexit 0\n"
}

// installProbe puts the store probe in dir and returns the ledger it logs to.
func installProbe(t *testing.T, e *harness.Env, dir string) string {
	t.Helper()
	ledger := filepath.Join(t.TempDir(), "log")
	e.FileGuard(dir, "control", harness.ControlGuard, map[string]string{"probe.sh": probeScript(ledger)})
	return ledger
}

// project is a git repository with the store probe installed and committed, so the
// rule's folder is part of the baseline and the first commit is its own.
func project(t *testing.T) (*harness.Env, string, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	ledger := installProbe(t, e, proj)
	e.CommitAll(proj, "the project before the session")
	return e, proj, ledger
}

// probeLines is the store probe's log.
func probeLines(t *testing.T, ledger string) []string {
	t.Helper()
	body, err := os.ReadFile(ledger)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// readsBack runs one cycle and requires that EVERY probe run it caused found
// the mark a previous hook stored — and that there was at least one.
//
// Every line, not the last. A cycle judging two new files runs the probe twice
// against one store, and the second run reads the first's mark whether or not
// that store is the conversation's: "the last line says yes" holds on a fresh
// store too. Only a FIRST probe run in the cycle reading "yes" proves the store
// was already written to before this cycle began — by an earlier transcript of
// the same conversation.
func readsBack(t *testing.T, ledger, what string, run func()) {
	t.Helper()
	before := len(probeLines(t, ledger))
	run()
	added := probeLines(t, ledger)[before:]
	if len(added) == 0 {
		t.Fatalf("%s: the probe never ran in this cycle, so nothing about the store can be concluded", what)
	}
	for i, l := range added {
		if l != "before=[yes]" {
			t.Fatalf("%s: probe run %d of this cycle read %q — the store it opened was not the one "+
				"the conversation wrote to before this continuation (this cycle: %q)", what, i+1, l, added)
		}
	}
}

// sameConversation requires that each session resolves, through the engine, to
// the origin record of the first.
func sameConversation(t *testing.T, e *harness.Env, proj string, sessions ...string) {
	t.Helper()
	want := e.OriginRecord(proj, sessions[0])
	if want == "" {
		t.Fatalf("%s has no origin record", sessions[0])
	}
	for _, s := range sessions {
		if got := e.SessionIdentity(proj, s); got != want {
			t.Errorf("%s resolves to %q, want the conversation's origin %q", s, got, want)
		}
	}
}

// T054_01 is the "chain revisits" mode: 110 real hook runs across four sessions
// of one conversation. The conversation compacted — the boundary appended to its
// own transcript — and each resume forked a new file opening on a copy of that
// same boundary, carrying a copy of the record it names. The forks' names sort
// ahead of the original's, as the real ones happened to, so a walk taking "any
// other file holding the logical parent" goes from one fork to the other and
// back, and the session runs with no store.
// sr:proves session/identity-survives-reissued-ids
func TestT054_01_ForksOfACompactedConversationKeepItsState(t *testing.T) {
	e, proj, ledger := project(t)

	e.Run(proj, "z-original", "start", Turns("done",
		Write("w1", "one.md", "first"),
		harness.Commit("k1", "first"),
		Compact("c1"),
	))
	e.RunForked(proj, "z-original", "a-fork-1", "resume once", Turns("done"))
	readsBack(t, ledger, "the second fork", func() {
		e.RunForked(proj, "z-original", "a-fork-2", "resume again", Turns("done",
			Write("w2", "two.md", "second"),
		).ThenCommit("second"))
	})
	sameConversation(t, e, proj, "z-original", "a-fork-1", "a-fork-2")
}

// T054_02 is the "continuation missing" mode that was not a lost file: 115 real
// hook runs across five forks. A preserved-segment compaction named a logical
// parent that no transcript holds; the file it compacted still holds the
// boundary itself, part-way down.
func TestT054_02_ABoundaryNamingAnUnwrittenParentKeepsState(t *testing.T) {
	e, proj, ledger := project(t)

	// A harness whose compaction names no parent (Codex's is a record of the same rollout, Cursor's
	// leaves no boundary) cannot name an unwritten one: its compaction is the plain one, which
	// names nothing, and the conversation must keep its state across it the same.
	boundary := Compact("c1")
	if harness.HasCap(t, harness.CapCompactionNamesParent) {
		boundary = CompactNamingUnwrittenParent("c1")
	}
	e.Run(proj, "orig-02", "start", Turns("done",
		Write("w1", "one.md", "first"),
		harness.Commit("k1", "first"),
		boundary,
	))
	readsBack(t, ledger, "the fork", func() {
		e.RunForked(proj, "orig-02", "fork-02", "resume", Turns("done",
			Write("w2", "two.md", "second"),
		).ThenCommit("second"))
	})
	sameConversation(t, e, proj, "orig-02", "fork-02")
}

// T054_03: the transcript a continuation came from is deleted while the
// continuation is closed — Claude Code deletes transcripts past its cleanup
// period while a later continuation stays resumable.
//
// Before the deletion the continuation is the conversation: same identity,
// same store, same baseline. After it the origin cannot be reached, so the
// continuation keys on its own root — a fresh store, whose baseline is taken at
// its first tool call on whatever HEAD is then — and keeps THAT across its own
// hooks. What was stored before is not carried over, and the session is told
// so exactly once, at its SessionStart.
func TestT054_03_AContinuationWhosePredecessorIsGoneKeepsItsOwnState(t *testing.T) {
	e, proj, ledger := project(t)
	start := e.Git(proj, "rev-parse", "HEAD")

	e.Run(proj, "orig-03", "start", Turns("done",
		Write("w1", "one.md", "first"),
		harness.Commit("b1", "work"),
		Compact("c1"),
	))
	moved := e.Git(proj, "rev-parse", "HEAD")
	if moved == start {
		t.Fatalf("the original session did not commit, so the baselines below cannot be told apart")
	}
	e.RunForked(proj, "orig-03", "fork-03", "resume", Turns("done"))

	// Before the deletion: one conversation, one store, the original baseline.
	sameConversation(t, e, proj, "orig-03", "fork-03")
	if got := e.Meta(proj, "fork-03", sessionstate.MetaBaselineCommit); got != start {
		t.Fatalf("before the deletion the continuation measures from %q, want the conversation's baseline %q", got, start)
	}

	e.DeleteTranscript(proj, "orig-03")

	// After it: the continuation's own root.
	if got, want := e.SessionIdentity(proj, "fork-03"), e.OriginRecord(proj, "fork-03"); got != want || want == "" {
		t.Fatalf("after the deletion the continuation resolves to %q, want its own root %q", got, want)
	}

	e.Run(proj, "fork-03", "carry on", Turns("done", Write("w2", "two.md", "second")).ThenCommit("second"))
	readsBack(t, ledger, "the continuation's next cycle", func() {
		e.Run(proj, "fork-03", "and more", Turns("done", Write("w3", "three.md", "third")).ThenCommit("third"))
	})

	// The fallback store is new, so its baseline is where HEAD was at its
	// first tool call — after the original session's commit, not before it.
	if got := e.Meta(proj, "fork-03", sessionstate.MetaBaselineCommit); got != moved {
		t.Errorf("the fallback store measures from %q, want HEAD at its first tool call %q", got, moved)
	}

	// Counted per hook run: each hook that printed is one report.
	reports := e.HookReports(proj, "fork-03", "sloprail: identity:")
	for _, rep := range reports {
		// A record that names no event (Codex's) cannot say which hook it was.
		if rep.Event != "" && rep.Event != "SessionStart" {
			t.Errorf("the fallback identity was reported by a %s hook, where nobody sees it", rep.Event)
		}
		if !strings.Contains(rep.Stdout, "earlier transcript is gone") {
			t.Errorf("the SessionStart report is not on stdout, the channel that is seen")
		}
	}
	if len(reports) != 1 {
		t.Errorf("the fallback identity must be reported by exactly one hook run, got %d", len(reports))
	}
}

// T054_04: a session resumed from another directory. Real Claude Code keeps
// appending to the transcript where the session began, but reports
// transcript_path under the new directory's project folder, where no file
// exists. Resumed from a subdirectory of the same repository, the session is
// the same tree, so the same store.
// sr:proves session/resume-from-another-directory
func TestT054_04_AResumeFromAnotherDirectoryKeepsState(t *testing.T) {
	e, proj, _ := project(t)
	sub := proj + "/sub"
	e.WriteFile(proj, "sub/.keep", "")
	// The rule lives at the root: a file-guard is judged by `sr-checks run` from the
	// project's folder, whichever directory the turn was resumed from.
	subLedger := installProbe(t, e, proj)
	e.CommitAll(proj, "sub")

	e.Run(proj, "moved-04", "start", Turns("done", Write("w1", "one.md", "first")).ThenCommit("first"))
	readsBack(t, subLedger, "the cycle resumed from below", func() {
		e.RunFrom(proj, "sub", "moved-04", "carry on from below", Turns("done",
			Write("w2", "two.md", "second"),
		).ThenCommit("second"))
	})

	if _, err := os.Stat(e.TranscriptPath(sub, "moved-04")); err == nil {
		if harness.HasCap(t, harness.CapResumeFromOtherDirectory) {
			t.Fatalf("the resumed turn was written under the new directory, so this is not the real shape")
		}
		// This harness names a conversation by an id of its own, whichever directory a turn
		// runs in: resumed from below, the session still resolves to the one it began as.
		if got, want := e.SessionIdentity(sub, "moved-04"), e.SessionIdentity(proj, "moved-04"); got != want || want == "" {
			t.Errorf("resumed from below, the session resolves to %q, want %q", got, want)
		}
		return
	}
	// Asked with the path the harness reports from below — which does not
	// exist — the engine finds the record where the session began.
	want := e.OriginRecord(proj, "moved-04")
	if got := e.SessionIdentity(sub, "moved-04"); got != want || want == "" {
		t.Errorf("resumed from below, the session resolves to %q, want its origin %q", got, want)
	}
	if got := e.SessionIdentity(proj, "moved-04"); got != want {
		t.Errorf("the session resolves to %q from its own directory, want %q", got, want)
	}
}
