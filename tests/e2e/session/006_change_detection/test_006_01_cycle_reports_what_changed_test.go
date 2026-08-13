package e2e

import (
	"strings"
	"testing"
)

// change_is_observed, difference_spans_both, untouched_stays_silent and
// turn_end_subjectless, through the wiring a user gets.
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
// TurnEnd, so a single run records everything the cycle dispatched.
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
  TurnEnd:
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

// T006_03: TurnEnd fires once, at the end, carrying no subject.
//
// turn_end_subjectless — the invariant with no coverage at all before this hook
// point existed, because nothing had ever dispatched the event.
//
// Three claims: it arrives, exactly one of it arrives however many files the
// cycle touched, and it names no file. The last is the one with a wrong answer
// available: a TurnEnd carrying a path would let a matcher narrow it to one
// file, and a rule about the cycle as a whole would then run per file.
func TestT006_03_TurnEndFiresOnceWithNoSubject(t *testing.T) {
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

	var turnEnds []string
	for _, l := range lines {
		if strings.Contains(l, `"kind":"TurnEnd"`) {
			turnEnds = append(turnEnds, l)
		}
	}

	if len(turnEnds) == 0 {
		t.Fatalf("TurnEnd never fired — the end of a cycle was never reported:\n%s", strings.Join(lines, "\n"))
	}
	if len(turnEnds) != 1 {
		t.Fatalf("TurnEnd fired %d times; a cycle ends once, however many files it touched:\n%s",
			len(turnEnds), strings.Join(turnEnds, "\n"))
	}
	// No subject. Read off the payload the hook actually received.
	if strings.Contains(turnEnds[0], `"path"`) {
		t.Errorf("TurnEnd carried a file — it is about the cycle, not about one file:\n%s", turnEnds[0])
	}

	// And the file events did fire, so "fired once" is a real constraint here
	// rather than something an empty cycle satisfied by accident.
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if !hasEvent(strings.Join(lines, "\n"), "PostFileCreate", name) {
			t.Errorf("no PostFileCreate for %q, so this cycle dispatched less than it should have", name)
		}
	}
}

// T006_04: a hook refusing a Post event does not block.
//
// before_refusable_only. The work has already landed, so a refusal at this
// timing demands a correction — it cannot prevent anything, and the engine must
// not claim a rollback it did not perform.
//
// The e2e for this was deleted while nothing dispatched Post events, because
// there was no way to reach a Post hook at all. This is it, rewritten against a
// hook point that now exists.
//
// Three things are asserted, and each closes a way the others could pass
// vacuously: the refusing hook RAN, the session still completed, and the file
// it objected to is still on disk.
func TestT006_04_APostRefusalDoesNotBlockTheWork(t *testing.T) {
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
	e.Guardrail(proj, "objects", refuseEveryCreate, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> refused.log\necho 'this file should not have been written' >&2\nexit 1\n",
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
	e.Guardrail(proj, "after", `---
hooks:
  TurnEnd:
    - hooks:
        - type: command
          command: ./record.sh
---

# Runs at the end of the cycle, after the objection
`, map[string]string{"record.sh": recordEvent})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")

	got := e.Run(proj, "s-006-04", "write a file", Turns("done",
		Write("w1", "unwanted.md", "it landed anyway"),
	))

	// The hook ran and refused. Without this the rest is a test that a file
	// exists after nothing tried to stop it.
	if ran := e.Ledger(proj, "objects", "refused.log"); len(ran) == 0 {
		t.Fatalf("the refusing Post hook never ran, so this proves nothing about after-the-fact refusals:\n%s", got.Output)
	}

	// The cycle carried on past the objection. This is the assertion that
	// separates "reported and continued" from "reported and abandoned".
	after := e.Ledger(proj, "after", "events.jsonl")
	if len(after) == 0 {
		t.Errorf("a Post hook's refusal stopped the rest of the cycle being dispatched — a refusal after the fact must be reported, not obeyed")
	}

	// The session completed. A Post refusal that blocked would leave the cycle
	// unable to end.
	if got.Code != 0 {
		t.Errorf("a Post refusal stopped the session completing (exit %d):\n%s", got.Code, got.Output)
	}

	// And the work is still there. "Prevented" is a claim about the tree, not
	// about what came back on a stream.
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
