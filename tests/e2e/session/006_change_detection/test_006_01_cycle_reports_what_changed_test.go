package e2e

import (
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
// Post event before this hook point was implemented, so every claim below was
// unreachable from outside the binary.
//
// A note on what the hooks write. A hook's working directory is its guardrail's
// folder, which sits INSIDE the tree being compared — so a hook recording what
// it saw is itself a change the next comparison reports. The guardrail is
// therefore committed before the session runs (see gitCommitGuardrail), and the
// assertions name the paths they expect rather than counting every event, so a
// ledger file appearing in a later cycle's difference cannot make a test lie.

// recordEvent appends the event it was handed, one line per run.
const recordEvent = `#!/bin/sh
cat >> events.jsonl
printf '\n' >> events.jsonl
`

// bindEverything binds one recording script to all three Post kinds and to
// Stop, so a single run records everything the cycle dispatched.
const bindEverything = `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
  PostFileUpdate:
    - hooks:
        - type: command
          command: ./record.sh
  PostFileDelete:
    - hooks:
        - type: command
          command: ./record.sh
  Stop:
    - hooks:
        - type: command
          command: ./record.sh
---

# Records every event the end of a cycle dispatches
`

// T006_01: what a cycle changed becomes Post events, one per file.
//
// The agent writes one file, edits a file the project already had, and deletes
// another — through ordinary tool calls, with nothing telling the engine which
// is which. The classification can only have come from comparing the tree
// against where the session began.
func TestT006_01_ACycleReportsWhatItChanged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// The project as it stood before the session: one file to update, one to
	// delete, and one nobody touches.
	e.WriteFile(proj, "existing.md", "before")
	e.WriteFile(proj, "doomed.md", "before")
	e.WriteFile(proj, "untouched.md", "before")
	e.Guardrail(proj, "records", bindEverything, map[string]string{"record.sh": recordEvent})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")

	e.Run(proj, "s-006-01", "change some files", Turns("done",
		Write("w1", "created.md", "new file"),
		Write("w2", "existing.md", "changed"),
		Bash("b1", "rm doomed.md"),
	))

	events := strings.Join(e.Ledger(proj, "records", "events.jsonl"), "\n")
	if events == "" {
		t.Fatalf("the end of the cycle dispatched nothing — no Post event reached a hook")
	}

	for _, want := range []struct{ kind, path string }{
		{"PostFileCreate", "created.md"},
		{"PostFileUpdate", "existing.md"},
		{"PostFileDelete", "doomed.md"},
	} {
		if !hasEvent(events, want.kind, want.path) {
			t.Errorf("no %s for %q in what the cycle dispatched:\n%s", want.kind, want.path, events)
		}
	}

	// untouched_stays_silent. A file the cycle never touched must not be
	// reported, or the first cycle in any real project buries the agent's work
	// under the rest of the repository.
	if strings.Contains(events, "untouched.md") {
		t.Errorf("a file the cycle never touched was reported:\n%s", events)
	}
}

// T006_02: work the agent COMMITTED is still the cycle's work.
//
// difference_spans_both, and the half a naive implementation loses. An agent
// that commits leaves a tree with nothing outstanding in it, so a comparison
// reading only what is outstanding reports that the cycle changed nothing —
// precisely wrong, and silently so.
//
// Both halves in one cycle, so the test cannot pass by covering either alone.
func TestT006_02_CommittedAndUncommittedWorkBothCount(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "records", bindEverything, map[string]string{"record.sh": recordEvent})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")

	e.Run(proj, "s-006-02", "write and commit", Turns("done",
		Write("w1", "committed.md", "this gets committed"),
		Bash("b1", "git add committed.md && git commit -m 'agent commit'"),
		Write("w2", "outstanding.md", "this does not"),
	))

	events := strings.Join(e.Ledger(proj, "records", "events.jsonl"), "\n")

	// The tree really is clean of the committed file, or this proves nothing:
	// it would be reported by any implementation that looked only at what is
	// outstanding.
	if status := e.Git(proj, "status", "--porcelain", "--", "committed.md"); status != "" {
		t.Fatalf("committed.md is still outstanding (%q), so this test does not exercise the committed half", status)
	}

	if !hasEvent(events, "PostFileCreate", "committed.md") {
		t.Errorf("work committed during the cycle fell out of the difference:\n%s", events)
	}
	if !hasEvent(events, "PostFileCreate", "outstanding.md") {
		t.Errorf("work left outstanding fell out of the difference:\n%s", events)
	}
}

// T006_03: Stop fires once, at the end, carrying no subject.
//
// stop_subjectless — the invariant with no coverage at all before this hook
// point existed, because nothing had ever dispatched the event.
//
// Three claims: it arrives, exactly one of it arrives however many files the
// cycle touched, and it names no file. The last is the one with a wrong answer
// available: a Stop carrying a path would let a matcher narrow it to one
// file, and a rule about the cycle as a whole would then run per file.
func TestT006_03_StopFiresOnceWithNoSubject(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "records", bindEverything, map[string]string{"record.sh": recordEvent})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")

	e.Run(proj, "s-006-03", "write several files", Turns("done",
		Write("w1", "a.md", "one"),
		Write("w2", "b.md", "two"),
		Write("w3", "c.md", "three"),
	))

	lines := e.Ledger(proj, "records", "events.jsonl")

	var stops []string
	for _, l := range lines {
		if strings.Contains(l, `"kind":"Stop"`) {
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
	// No subject. Read off the payload the hook actually received.
	if strings.Contains(stops[0], `"path"`) {
		t.Errorf("Stop carried a file — it is about the cycle, not about one file:\n%s", stops[0])
	}

	// And the file events did fire, so "fired once" is a real constraint here
	// rather than something an empty cycle satisfied by accident.
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if !hasEvent(strings.Join(lines, "\n"), "PostFileCreate", name) {
			t.Errorf("no PostFileCreate for %q, so this cycle dispatched less than it should have", name)
		}
	}
}

// T006_04: a hook refusing a Post event cannot undo the write, and DOES stop
// the turn from ending.
//
// Two different things, and conflating them was a real design error in this
// work before the owner corrected it:
//
//   - The write cannot be undone. The file is on disk and the cycle is over;
//     an engine claiming otherwise would be promising a rollback it never
//     performed. That is what before_refusable_only is about — preventing the
//     ACTION — and it is why the file assertion below expects the file to
//     survive.
//   - The turn must not end. That is what Claude's Stop hook is for, and it is
//     the entire mechanism by which an after-the-fact rule gets a correction
//     rather than merely complaining once. A refusal printed to stderr with
//     exit 0 is a refusal nobody sees and nothing acts on.
//
// What this test now distinguishes, since the file surviving is no longer the
// discriminator: it asserts the cycle BLOCKED, that the reason ARRIVED with the
// block, and that the rest of the cycle was still dispatched. The measured
// channel table is in the probe that produced it — of the channels that block,
// several deliver no text at all, so "blocked" and "the agent was told why" are
// separate facts and a test asserting only the first cannot tell a useful
// refusal from an agent stuck with no idea what to fix.
func TestT006_04_APostRefusalBlocksTheTurnWithoutUndoingTheWork(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	const refuseEveryCreate = `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh
---

# Objects to every file created, after the fact
`
	// The hook records that it ran OUTSIDE the project, into a directory of
	// this test's own.
	//
	// A hook's working directory is its guardrail's folder, which sits inside
	// the tree the engine compares — so a hook logging there creates an
	// untracked file, which is itself a change the same cycle reports and this
	// same rule then refuses. One rule objecting to one file quietly became two
	// objections, which took the multi-refusal path and made the single-refusal
	// wording untested.
	ranLog := filepath.Join(t.TempDir(), "ran.log")
	e.Guardrail(proj, "zqguard", refuseEveryCreate, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> " + ranLog + "\necho 'this file should not have been written' >&2\nexit 1\n",
	})
	// A SECOND guardrail, bound to the end of the cycle, which the first one's
	// refusal must not silence.
	//
	// Without it this test cannot fail for the right reason. The file survives a
	// refusal whatever the engine does — a Post event describes work that has
	// already landed, so nothing was ever going to remove it — which means "the
	// file is still there" passes just as well against an engine that abandoned
	// the rest of the cycle the moment a hook objected. What distinguishes the
	// two is whether anything AFTER the refusal still happened, and that needs a
	// rule bound after it to observe. Confirmed by mutation: with the dispatcher
	// made to stop at the first refusal, the file assertion below still passed
	// and only this one caught it.
	afterLog := filepath.Join(t.TempDir(), "after.log")
	e.Guardrail(proj, "after", `---
hooks:
  Stop:
    - hooks:
        - type: command
          command: ./record.sh
---

# Runs at the end of the cycle, after the objection
`, map[string]string{"record.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> " + afterLog + "\nexit 0\n"})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")

	got := e.Run(proj, "s-006-04", "write a file", Turns("done",
		Write("w1", "unwanted.md", "it landed anyway"),
	))

	// The hook ran and refused. Without this the rest is a test about a file
	// existing after nothing tried to stop it.
	if _, err := os.Stat(ranLog); err != nil {
		t.Fatalf("the refusing Post hook never ran, so this proves nothing about after-the-fact refusals:\n%s", got.Output)
	}

	// The turn was blocked. A blocked stop makes the agent continue past its
	// own end, so the mock is driven round again and emits its final result
	// more than once — one result means the turn simply ended.
	//
	// This is the assertion the old version of this test lacked, and the file
	// check below could never have supplied: the file survives whether the
	// engine blocks or silently permits.
	if n := strings.Count(got.Output, `"subtype":"success"`); n < 2 {
		t.Errorf("the turn ended despite a guardrail refusing (%d result lines) — a Post refusal must stop the turn, which is the only way it gets anything corrected:\n%s", n, got.Output)
	}

	// The agent was told WHY. Separate from the block: of the channels that
	// block a Stop, several deliver no text, so an engine can leave the agent
	// stopped with no idea which rule objected or what to fix — worse than
	// permitting, because now it is stuck as well as uninformed.
	//
	// Read from the blocking attachments the refusal produced, NOT from the
	// record as a whole. A guardrail's own folder path travels on every hook
	// payload, so searching the file for the rule's name finds it whether or not
	// the refusal ever named it — an assertion that cannot fail.
	blocking := e.BlockingErrors(proj, "s-006-04")
	if len(blocking) == 0 {
		t.Fatalf("the turn was blocked but no reason reached the agent — it is stopped with nothing to act on")
	}
	told := strings.Join(blocking, "\n")
	if !strings.Contains(told, "this file should not have been written") {
		t.Errorf("the hook's own words did not reach the agent, so it cannot know what to fix:\n%s", told)
	}
	// The guardrail's name, inside the refusal itself. An agent told only that
	// it was blocked cannot find the rule it broke.
	if !strings.Contains(told, "zqguard") {
		t.Errorf("the refusal did not name the guardrail that produced it:\n%s", told)
	}

	// The rest of the cycle was still dispatched. One rule objecting must not
	// silence the rules bound after it — an agent fixing violations one turn at
	// a time is the slow version of the same bug.
	if _, err := os.Stat(afterLog); err != nil {
		t.Errorf("a Post hook's refusal stopped the rest of the cycle being dispatched")
	}

	// And the work is still there. The refusal demands a correction; it does not
	// and cannot perform one.
	if !e.Exists(proj, "unwanted.md") {
		t.Errorf("a hook refusing AFTER the write removed the file — an after-the-fact refusal must demand a correction, not perform one")
	}
}

// T006_05: a file the agent created and never staged is still the cycle's work.
//
// `git diff` compares tracked content, so an untracked file appears in no diff
// at all — an implementation asking only that question reports nothing for
// exactly the files an agent writing scratch output produces.
func TestT006_05_UntrackedWorkIsReported(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "records", bindEverything, map[string]string{"record.sh": recordEvent})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")

	e.Run(proj, "s-006-05", "write a scratch file", Turns("done",
		Write("w1", "scratch.md", "never staged"),
	))

	// The control: git really is not tracking it, so the diff alone cannot have
	// been what found it.
	if tracked := e.Git(proj, "ls-files", "--", "scratch.md"); tracked != "" {
		t.Fatalf("scratch.md is tracked (%q), so this test does not exercise the untracked half", tracked)
	}

	events := strings.Join(e.Ledger(proj, "records", "events.jsonl"), "\n")
	if !hasEvent(events, "PostFileCreate", "scratch.md") {
		t.Errorf("an untracked file the agent created was never reported:\n%s", events)
	}
}

// T006_06: every refusal in a cycle reaches the agent at once.
//
// Collected, not returned at the first. An agent handed one violation per turn
// spends as many turns as there are rules — and the rules bound after the first
// refusal would not have run at all, so it could not even see what else is
// wrong.
//
// Two guardrails, both refusing, both named in the one blocking reason.
func TestT006_06_EveryRefusalReachesTheAgentAtOnce(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	for _, name := range []string{"alpharule", "betarule"} {
		e.Guardrail(proj, name, `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses every created file
`, map[string]string{"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho 'objection from " + name + "' >&2\nexit 1\n"})
	}
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")

	e.Run(proj, "s-006-06", "write a file", Turns("done",
		Write("w1", "f.md", "x"),
	))

	blocking := e.BlockingErrors(proj, "s-006-06")
	if len(blocking) == 0 {
		t.Fatalf("two guardrails refused and the turn was not blocked")
	}
	told := blocking[0]
	if !strings.Contains(told, "alpharule") {
		t.Errorf("the first guardrail's objection is missing from what the agent was told:\n%s", told)
	}
	if !strings.Contains(told, "betarule") {
		t.Errorf("the second guardrail's objection is missing — blocking at the first hides the rest:\n%s", told)
	}
}

// hasEvent reports whether the recorded events hold one of the given kind
// naming the given path.
//
// Both on ONE line, which is what makes it an assertion about a single event: a
// cycle dispatching a create for one file and a delete for another would
// satisfy any test that looked for the kind and the path separately.
func hasEvent(events, kind, path string) bool {
	for _, line := range strings.Split(events, "\n") {
		if strings.Contains(line, `"kind":"`+kind+`"`) && strings.Contains(line, `"path":"`+path+`"`) {
			return true
		}
	}
	return false
}
