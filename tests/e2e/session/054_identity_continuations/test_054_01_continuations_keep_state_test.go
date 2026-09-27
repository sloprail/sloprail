package e2e

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// project is a git repository with the store probe installed.
func project(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "control", harness.ControlGuard, map[string]string{"probe.sh": harness.ControlScript})
	e.GitInit(proj)
	return e, proj
}

// probe returns the store probe's log lines from dir's copy of the guard.
func probe(e *harness.Env, dir string) []string {
	return e.FileGuardLedgerLines(dir, "control", "log")
}

// lastProbe is the most recent line the probe logged.
func lastProbe(t *testing.T, e *harness.Env, dir string) string {
	t.Helper()
	lines := probe(e, dir)
	if len(lines) == 0 {
		t.Fatalf("the probe never ran, so nothing about the store can be concluded")
	}
	return lines[len(lines)-1]
}

// T054_01 is the "chain revisits" mode: 110 real hook runs across four sessions
// of one conversation. The conversation compacted — the boundary appended to its
// own transcript — and each resume forked a new file opening on a copy of that
// same boundary, carrying a copy of the record it names. The forks' names sort
// ahead of the original's, as the real ones happened to, so a walk taking "any
// other file holding the logical parent" goes from one fork to the other and
// back, and the session runs with no store.
func TestT054_01_ForksOfACompactedConversationKeepItsState(t *testing.T) {
	e, proj := project(t)

	e.Run(proj, "z-original", "start", Turns("done",
		Write("w1", "one.md", "first"),
		Compact("c1"),
	))
	if got := lastProbe(t, e, proj); !strings.Contains(got, "before=[") {
		t.Fatalf("the probe did not run in the original session: %q", got)
	}
	e.RunForked(proj, "z-original", "a-fork-1", "resume once", Turns("done"))
	e.RunForked(proj, "z-original", "a-fork-2", "resume again", Turns("done",
		Write("w2", "two.md", "second"),
	))

	if got := lastProbe(t, e, proj); got != "before=[yes]" {
		t.Fatalf("the second fork did not read what the conversation stored before it forked: %q "+
			"(every line: %q)", got, probe(e, proj))
	}
	id := e.SessionIdentity(proj, "z-original")
	for _, s := range []string{"a-fork-1", "a-fork-2"} {
		if got := e.SessionIdentity(proj, s); got != id || id == "" {
			t.Errorf("fork %s resolves to %q, the conversation to %q — forks must converge", s, got, id)
		}
	}
}

// T054_02 is the "continuation missing" mode that was not a lost file: 115 real
// hook runs across five forks. A preserved-segment compaction named a logical
// parent that no transcript holds; the file it compacted still holds the
// boundary itself, part-way down.
func TestT054_02_ABoundaryNamingAnUnwrittenParentKeepsState(t *testing.T) {
	e, proj := project(t)

	e.Run(proj, "orig-02", "start", Turns("done",
		Write("w1", "one.md", "first"),
		CompactNamingUnwrittenParent("c1"),
	))
	e.RunForked(proj, "orig-02", "fork-02", "resume", Turns("done",
		Write("w2", "two.md", "second"),
	))

	if got := lastProbe(t, e, proj); got != "before=[yes]" {
		t.Fatalf("the fork did not read what the conversation stored before it: %q (every line: %q)",
			got, probe(e, proj))
	}
}

// T054_03: the transcript a continuation came from is gone — Claude Code
// deletes transcripts past its cleanup period while a later continuation stays
// resumable. The origin cannot be reached, so what was stored before the
// continuation is not carried over; but the session must still have a store,
// the same one on every one of its own hooks, and say so once.
func TestT054_03_AContinuationWhosePredecessorIsGoneKeepsItsOwnState(t *testing.T) {
	e, proj := project(t)

	e.Run(proj, "orig-03", "start", Turns("done",
		Write("w1", "one.md", "first"),
		Compact("c1"),
	))
	e.RunForked(proj, "orig-03", "fork-03", "resume", Turns("done"))
	e.DeleteTranscript(proj, "orig-03")

	e.Run(proj, "fork-03", "carry on", Turns("done", Write("w2", "two.md", "second")))
	e.Run(proj, "fork-03", "and more", Turns("done", Write("w3", "three.md", "third")))

	if got := lastProbe(t, e, proj); got != "before=[yes]" {
		t.Fatalf("a continuation whose predecessor is gone did not keep state across its own hooks: %q "+
			"(every line: %q)", got, probe(e, proj))
	}
	record, err := os.ReadFile(e.TranscriptPath(proj, "fork-03"))
	if err != nil {
		t.Fatal(err)
	}
	// Counted per hook run: each hook that printed leaves one attachment, and
	// the plugin's start hook repeats sr-session's report on stdout, so the
	// words can appear more than once inside a single attachment.
	reports := 0
	for _, l := range strings.Split(string(record), "\n") {
		var rec struct {
			Attachment struct {
				Stderr string `json:"stderr"`
			} `json:"attachment"`
		}
		if json.Unmarshal([]byte(l), &rec) == nil && strings.Contains(rec.Attachment.Stderr, "earlier transcript is gone") {
			reports++
		}
	}
	if reports != 1 {
		t.Errorf("the fallback identity must be reported by exactly one hook run, got %d", reports)
	}
}

// T054_04: a session resumed from another directory. Real Claude Code keeps
// appending to the transcript where the session began, but reports
// transcript_path under the new directory's project folder, where no file
// exists. Resumed from a subdirectory of the same repository, the session is
// the same tree, so the same store.
func TestT054_04_AResumeFromAnotherDirectoryKeepsState(t *testing.T) {
	e, proj := project(t)
	e.WriteFile(proj, "sub/.keep", "")
	e.FileGuard(proj+"/sub", "control", harness.ControlGuard, map[string]string{"probe.sh": harness.ControlScript})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "sub")

	e.Run(proj, "moved-04", "start", Turns("done", Write("w1", "one.md", "first")))
	e.RunFrom(proj, "sub", "moved-04", "carry on from below", Turns("done",
		Write("w2", "two.md", "second"),
	))

	if _, err := os.Stat(e.TranscriptPath(proj+"/sub", "moved-04")); err == nil {
		t.Fatalf("the resumed turn was written under the new directory, so this is not the real shape")
	}
	if got := lastProbe(t, e, proj+"/sub"); got != "before=[yes]" {
		t.Fatalf("the session resumed from a subdirectory did not read what it stored before: %q "+
			"(every line: %q)", got, probe(e, proj+"/sub"))
	}
}
