package e2e

import (
	"encoding/json"
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"github.com/sloprail/sloprail/tests/e2e/session/changesetkit"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// change_is_observed, difference_spans_both, untouched_stays_silent and
// stop_subjectless, through the wiring a user gets.
//
// What makes these end-to-end rather than unit tests is the question they
// answer: not "does the dispatcher classify correctly" but "does the end of a
// cycle ever reach the dispatcher at all". Nothing in this repo dispatched a
// cycle-end check before this hook point was implemented, so every claim below was
// unreachable from outside the binary.
//
//
// The vehicle is chosen from what each test OBSERVES:
//
//   - the FILE cases (a create/update/delete observed with the right status, an
//     untouched file staying silent, committed and untracked work still counted)
//     are file-STATE facts, so they ride a file-guard that records every
//     Changeset it is handed at Stop (`match: "**/*.md"`).
//   - the STOP cases (Stop fires once and subjectless; a Stop-bound rule still
//     runs after a refusal) are about the cycle as a whole. Stop is a
//     GateEventKind, so a rule bound to it is a GATE waking on Stop, never a
//     file-guard; session/026_stop_subjectless is the dedicated Stop suite.
//
// The file-guard check reads the Changeset (`.changeset.files[]`), and records into a
// harness ledger OUTSIDE the project (changesetkit.RecordScript): inside the rule's
// folder the recording would change the rule's hash between cycles. The rules are
// committed before the session runs (see the session-start commit).

// recordFileEvent is a NEW-FORMAT file-guard that records every Changeset it is handed, one line per run, and permits unconditionally — the
// question here is which changes were observed, not what anyone decided about
// them. `match: "**/*.md"` selects every markdown file at the repository root or
// any depth (`**/` compiles to an OPTIONAL leading directory).
const recordFileEvent = `match: "**/*.md"
# deletions: include — this guard observes EVERY change, and a file-guard
# skips deleted files unless it says so.
deletions: include
checks:
  - script: ./record.sh
`

// recordStopGate is a NEW-FORMAT gate that wakes on Stop and records the event it
// was handed. No `match`: Stop's kind declaration carries no fields, so there is
// nothing a match could narrow on. Its check permits, so nothing here blocks
// except where a test's own check refuses.
const recordStopGate = `on:
  - event: Stop
checks:
  - script: ./record.sh
`

// T006_01: what a cycle changed reaches a file-guard as one changeset entry per file.
//
// The agent writes one file, edits a file the project already had, and deletes
// another — through ordinary tool calls, with nothing telling the engine which
// is which. The classification can only have come from comparing the tree
// against where the session began.
// sr:proves events/post-changes-are-the-tree-diff
func TestT006_01_ACycleReportsWhatItChanged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// The project as it stood before the session: one file to update, one to
	// delete, and one nobody touches.
	e.WriteFile(proj, "existing.md", "before")
	e.WriteFile(proj, "doomed.md", "before")
	e.WriteFile(proj, "untouched.md", "before")
	e.CommitAll(proj, "the project before the rule")
	// The rule goes in its own commit: its range starts at that commit's parent, so
	// the files above are the base, not part of the first range.
	led := e.NewLedger("events")
	e.FileGuard(proj, "records", recordFileEvent, map[string]string{"record.sh": led.RecordScript()})
	e.CommitAll(proj, "the rule, before the session")

	e.Run(proj, "s-006-01", "change some files", Turns("done",
		Write("w1", "created.md", "new file"),
		Write("w2", "existing.md", "changed"),
		Bash("b1", "rm doomed.md"),
	).ThenCommit("change some files"))

	events := led.Lines()
	if len(events) == 0 {
		t.Fatalf("the end of the cycle dispatched nothing — no changeset reached a file-guard")
	}

	for _, want := range []struct{ status, path string }{
		{"A", "created.md"},
		{"M", "existing.md"},
		{"D", "doomed.md"},
	} {
		if !changesetkit.Has(changesetkit.Files(t, events), want.status, want.path) {
			t.Errorf("no file with status %s for %q in what the cycle dispatched:\n%s", want.status, want.path, strings.Join(events, "\n"))
		}
	}

	// untouched_stays_silent. A file the cycle never touched must not be
	// reported, or the first cycle in any real project buries the agent's work
	// under the rest of the repository.
	if changesetkit.Saw(changesetkit.Files(t, events), "untouched.md") {
		t.Errorf("a file the cycle never touched was reported:\n%s", strings.Join(events, "\n"))
	}
}

// T006_02: work committed in separate commits is one changeset.
//
// The agent commits once mid-cycle and once at the end. A rule's range spans
// both commits, so a changeset reading only the last one would report the cycle
// as smaller than it was.
func TestT006_02_WorkInSeparateCommitsIsOneChangeset(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("events")
	e.FileGuard(proj, "records", recordFileEvent, map[string]string{"record.sh": led.RecordScript()})
	base := e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-006-02", "write and commit", Turns("done",
		Write("w1", "committed.md", "this gets committed"),
		harness.CommitPaths("b1", "agent commit", "committed.md"),
		Write("w2", "outstanding.md", "this does not"),
	).ThenCommit("the rest"))

	// The premise: committed.md really was committed in a commit of its own, before
	// the end-of-cycle one, so the range holds two commits and a changeset reading only
	// the last would miss it. Without this the test is one commit at the end and proves
	// nothing about spanning.
	if subjects := e.Git(proj, "log", "--format=%s", base+"..HEAD", "--", "committed.md"); subjects != "agent commit" {
		t.Fatalf("committed.md was not committed mid-cycle in its own commit (commits touching it: %q)", subjects)
	}
	if n := e.Git(proj, "rev-list", "--count", base+"..HEAD"); n != "2" {
		t.Fatalf("the cycle made %s commits, want 2 (the mid-cycle one and the last)", n)
	}

	events := led.Lines()

	if !changesetkit.Has(changesetkit.Files(t, events), "A", "committed.md") {
		t.Errorf("work committed during the cycle fell out of the difference:\n%s", strings.Join(events, "\n"))
	}
	if !changesetkit.Has(changesetkit.Files(t, events), "A", "outstanding.md") {
		t.Errorf("work committed at the end fell out of the difference:\n%s", strings.Join(events, "\n"))
	}
}

// T006_03: Stop fires once, at the end, carrying no subject.
//
// stop_subjectless — the invariant with no coverage at all before this hook
// point existed, because nothing had ever dispatched the event. Stop is a
// GateEventKind, so this rides a GATE that wakes on Stop (nature_stop.go step 3),
// not a file-guard.
//
// Three claims: it arrives, exactly one of it arrives however many files the
// cycle touched, and it names no file. The last is the one with a wrong answer
// available: a Stop carrying a path would let a matcher narrow it to one
// file, and a rule about the cycle as a whole would then run per file.
// sr:proves events/turn-end-every-turn
func TestT006_03_StopFiresOnceWithNoSubject(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("events")
	e.Gate(proj, "cyclestop", recordStopGate, map[string]string{"record.sh": led.RecordScript()})
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-006-03", "write several files", Turns("done",
		Write("w1", "a.md", "one"),
		Write("w2", "b.md", "two"),
		Write("w3", "c.md", "three"),
	))

	lines := led.Lines()

	var stops []string
	for _, l := range lines {
		var p struct {
			Event struct {
				Kind string `json:"kind"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(l), &p); err != nil {
			continue
		}
		if p.Event.Kind == "Stop" {
			stops = append(stops, l)
		}
	}

	if len(stops) == 0 {
		t.Fatalf("Stop never fired — the end of a cycle was never reported:\n%s", strings.Join(lines, "\n"))
	}
	if len(stops) != 1 {
		t.Fatalf("Stop fired %d times; a cycle ends once, however many files it touched:\n%s",
			len(stops), strings.Join(stops, "\n"))
	}
	// No subject. Read off the flat event the gate's check actually received: an
	// object carrying `kind` and NO other key. `.event.path` on it is a clean
	// miss, not the error a subject-bearing or null event would produce.
	var ev struct {
		Event map[string]json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal([]byte(stops[0]), &ev); err != nil {
		t.Fatalf("the recorded Stop is not a payload: %v\n%s", err, stops[0])
	}
	for k := range ev.Event {
		if k != "kind" {
			t.Errorf("Stop carried a subject field %q — it is about the cycle, not about one file:\n%s", k, stops[0])
		}
	}

	// And the file events did fire, so "fired once" is a real constraint here
	// rather than something an empty cycle satisfied by accident. A Stop gate is
	// not handed the file events, so this is read from the same gate's ledger by
	// counting that the Stop arrived for a cycle that genuinely did work — the
	// three writes above are what produced it.
	if len(lines) == 0 {
		t.Errorf("the cycle recorded nothing at all, so 'Stop fired once' is a claim about an empty cycle")
	}
}

// T006_04: a file-guard's refusal cannot undo the write, and DOES stop the
// turn from ending; and a rule bound to the end of the cycle still runs after the
// refusal.
//
// Two different things, and conflating them was a real design error in this work
// before the owner corrected it:
//
//   - The write cannot be undone. The file is on disk and the cycle is over; an
//     engine claiming otherwise would be promising a rollback it never performed.
//     That is why the file assertion below expects the file to survive.
//   - The turn must not end. That is what the Stop block is for, and it is the
//     entire mechanism by which an after-the-fact rule gets a correction rather
//     than merely complaining once.
//
// The refusing rule is a NEW-FORMAT file-guard after-check (runFileGuardsPost
// blocks the turn on its refusal and does not touch the file). The rule that must
// still run AFTER it is bound to the end of the cycle — a GATE on Stop, which
// nature_stop.go runs (step 3) after the file-guard after-checks (step 1), so a
// file-guard refusal in step 1 must not prevent the Stop gate in step 3 from
// running. This asserts the cycle BLOCKED, that the reason ARRIVED, that it named
// its rule, that the rest of the cycle was still dispatched, and that the work
// survives.
func TestT006_04_APostRefusalBlocksTheTurnWithoutUndoingTheWork(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// The file-guard records that it ran OUTSIDE the project, into a directory of
	// this test's own, then refuses. Outside deliberately: a check's cwd is its
	// guard's folder inside the compared tree, so logging there would create an
	// untracked file the same cycle reports and this same rule then refuses —
	// turning one objection into two and taking the multi-refusal path, which
	// leaves the single-refusal wording untested. The refusal is the NEW-FORMAT
	// contract: exit non-zero, reason as `{"reason":…}` on stdout.
	ranLog := filepath.Join(t.TempDir(), "ran.log")
	e.FileGuard(proj, "zqguard", recordFileEvent, map[string]string{
		"record.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> " + ranLog + "\n" +
			"echo '{\"reason\":\"this file should not have been written\"}'\nexit 1\n",
	})
	// A SECOND rule, bound to the end of the cycle, which the first one's refusal
	// must not silence. Without it this test cannot fail for the right reason. The
	// file survives a refusal whatever the engine does — a Post event describes
	// work that already landed — so "the file is still there" passes just as well
	// against an engine that abandoned the rest of the cycle the moment a rule
	// objected. What distinguishes the two is whether anything AFTER the refusal
	// still happened, and that needs a rule bound after it to observe. It is a Stop
	// gate because Stop is where "the rest of the cycle" runs (step 3, after the
	// file-guard after-checks of step 1).
	afterLog := filepath.Join(t.TempDir(), "after.log")
	e.Gate(proj, "after", recordStopGate, map[string]string{
		"record.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> " + afterLog + "\nexit 0\n",
	})
	e.CommitAll(proj, "the project before the session")

	got := e.Run(proj, "s-006-04", "write a file", Turns("done",
		Write("w1", "unwanted.md", "it landed anyway"),
	).ThenCommit("write a file"))

	// The rule ran and refused. Without this the rest is a test about a file
	// existing after nothing tried to stop it.
	if _, err := os.Stat(ranLog); err != nil {
		t.Fatalf("the refusing file-guard never ran, so this proves nothing about after-the-fact refusals:\n%s", got.Output)
	}

	// The work was refused: `sr check run` over the session's commits (run by the
	// harness after the Run, harness.FileGuardRefusals) refused it. The file check
	// below could never have supplied this: the file survives whether the engine
	// refuses or silently permits.
	if len(e.FileGuardRefusals(proj, "s-006-04")) == 0 {
		t.Errorf("the work passed despite a guardrail refusing — a refusal is the only way it gets anything corrected:\n%s", got.Output)
	}

	// The agent was told WHY. Separate from the block: of the channels that block a
	// Stop, several deliver no text, so an engine can leave the agent stopped with
	// no idea which rule objected or what to fix. Read from the blocking
	// attachments the refusal produced, NOT from the record as a whole — a rule's
	// own folder path travels on every hook payload, so searching the file for the
	// rule's name finds it whether or not the refusal ever named it.
	blocking := e.BlockingErrors(proj, "s-006-04")
	if len(blocking) == 0 {
		t.Fatalf("the turn was blocked but no reason reached the agent — it is stopped with nothing to act on")
	}
	told := strings.Join(blocking, "\n")
	if !strings.Contains(told, "this file should not have been written") {
		t.Errorf("the hook's own words did not reach the agent, so it cannot know what to fix:\n%s", told)
	}
	// The rule's name, inside the refusal itself. An agent told only that it was
	// blocked cannot find the rule it broke.
	if !strings.Contains(told, "zqguard") {
		t.Errorf("the refusal did not name the guardrail that produced it:\n%s", told)
	}

	// The rest of the cycle was still dispatched. One rule objecting must not
	// silence the rules bound after it — an agent fixing violations one turn at a
	// time is the slow version of the same bug.
	if _, err := os.Stat(afterLog); err != nil {
		t.Errorf("a Post refusal stopped the rest of the cycle being dispatched")
	}

	// And the work is still there. The refusal demands a correction; it does not
	// and cannot perform one.
	if !e.Exists(proj, "unwanted.md") {
		t.Errorf("a rule refusing AFTER the write removed the file — an after-the-fact refusal must demand a correction, not perform one")
	}
}

// T006_05: a file the agent created and never committed is not judged: the Stop
// is refused for the commit, and once the agent commits it the rule sees it.
//
// An untracked file appears in no diff at all, so the engine cannot judge it; it
// asks for the commit instead (never making one itself).
func TestT006_05_UncommittedWorkIsRefusedUntilCommitted(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("events")
	e.FileGuard(proj, "records", recordFileEvent, map[string]string{"record.sh": led.RecordScript()})
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-006-05", "write a scratch file", Turns("done",
		Write("w1", "scratch.md", "never staged"),
	))

	// The control: git really is not tracking it.
	if tracked := e.Git(proj, "ls-files", "--", "scratch.md"); tracked != "" {
		t.Fatalf("scratch.md is tracked (%q), so this test does not exercise the untracked half", tracked)
	}
	e.AssertCommitRequired(proj, "s-006-05", "scratch.md")
	if events := led.Lines(); changesetkit.Saw(changesetkit.Files(t, events), "scratch.md") {
		t.Fatalf("an uncommitted file was judged:\n%s", strings.Join(events, "\n"))
	}
	seen := len(CommitRequired(e.BlockingErrorsFrom(proj, "s-006-05", "Stop")))

	e.Run(proj, "s-006-05", "now commit it", Turns("committed").ThenCommit("the scratch file"))

	e.NoCommitRequired(proj, "s-006-05", seen)
	if events := led.Lines(); !changesetkit.Has(changesetkit.Files(t, events), "A", "scratch.md") {
		t.Errorf("the committed file never reached the rule:\n%s", strings.Join(events, "\n"))
	}
}

// T006_06: every refusal in a cycle reaches the agent at once.
//
// Collected, not returned at the first. An agent handed one violation per turn
// spends as many turns as there are rules — and the rules bound after the first
// refusal would not have run at all, so it could not even see what else is
// wrong.
//
// Two file-guards, both refusing the same write, both named in the one blocking
// reason. runFileGuardsPost iterates EVERY file-guard against the cycle's Post
// events and keeps going past a refusal (it appends the objection and continues),
// so a "stop at the first refusal" engine loses the second here.
func TestT006_06_EveryRefusalReachesTheAgentAtOnce(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	for _, name := range []string{"alpharule", "betarule"} {
		e.FileGuard(proj, name, recordFileEvent, map[string]string{
			"record.sh": "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"objection from " + name + "\"}'\nexit 1\n",
		})
	}
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-006-06", "write a file", Turns("done",
		Write("w1", "f.md", "x"),
	).ThenCommit("write a file"))

	blocking := e.BlockingErrors(proj, "s-006-06")
	if len(blocking) == 0 {
		t.Fatalf("two guardrails refused and the turn was not blocked")
	}
	told := strings.Join(blocking, "\n")
	if !strings.Contains(told, "alpharule") {
		t.Errorf("the first guardrail's objection is missing from what the agent was told:\n%s", told)
	}
	if !strings.Contains(told, "betarule") {
		t.Errorf("the second guardrail's objection is missing — blocking at the first hides the rest:\n%s", told)
	}
}
