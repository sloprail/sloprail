package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A sub-agent's refusal reaches the conversation.
//
// This is the claim T013_01 had to give up. It asserted that a delegated cycle
// completes with the plugin bound, and its own note recorded why it could say no
// more: mutate `subagent-stop` to refuse on every cycle and the whole package
// stayed green, so exit 0, 1 and 2 were alike invisible and a rule refusing a
// sub-agent's work could not be shown to do anything at all.
//
// Two things were wrong, in two repositories, and the gap needed both.
//
//  1. The mock ran the SubagentStop block loop but never RECORDED the block.
//     Its Stop has always written a hook_blocking_error attachment carrying the
//     refusal's text — the channel harness.BlockingErrors reads, and the only
//     one a refusal's words travel down. SubagentStop wrote nothing but a line
//     on the mock's own stderr, so from outside, a refused delegated cycle and
//     a clean one were the same conversation.
//
//  2. The mock seeded a sub-agent's transcript with a record carrying NO uuid.
//     A real Claude sub-agent transcript opens with a uuid-bearing record whose
//     parentUuid is null — the origin the engine keys a stable identity by —
//     and every later record chains from it (verified against a real one on
//     disk, not assumed). Without the uuid the engine found no origin at all,
//     reported "every entry has a parent", and STOOD DOWN rather than judging a
//     cycle it could not place. That stand-down is correct behaviour on a
//     malformed record; the record was the bug.
//
//  3. `sloprail session subagent-stop` never refused anything. It resolved the
//     sub-agent's identity and then returned nil — the tree-diffing and dispatch
//     were stubbed. So even with a channel and an identity there was nothing to
//     send. It now runs completeCycle, the same function the root's Stop runs,
//     against the sub-agent's own payload.
//
// None of the three is observable alone, which is why the earlier measurement
// read as a harness limit: the engine was silent, the identity it needed was
// unresolvable, and the channel that would have carried either was missing.
// Each mutation is recorded on the assertion it kills.

// T013_06: a guardrail refusing a sub-agent's work blocks that sub-agent's stop,
// and its words reach the conversation.
//
// The sub-agent shares the dispatching session's tree rather than taking a
// worktree, because the refusal has to be about work that really landed. The
// mock does not APPLY a sub-agent's tool calls — a Write in a sub-agent's
// scenario creates no file — so the delegated work is done with Bash, which the
// mock does execute, in the tree the guardrail is watching.
//
// Asserted against the SUBAGENT STOP attachments specifically, not against every
// refusal in the record. Sharing the tree means the root's own Stop sees
// from-sub.md too and refuses it a second time, so "some refusal arrived" is
// true even when the sub-agent's cycle judged nothing — measured, by re-stubbing
// subagent-stop and watching the loose version of this test stay green.
func TestT013_06_ASubagentRefusalReachesTheConversation(t *testing.T) {
	e := New(t)
	proj := e.Project()

	ranLog := filepath.Join(t.TempDir(), "refused.log")
	e.Guardrail(proj, "nosubwork", refuseCreatedFiles, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> " + ranLog + "\n" +
			"echo 'the sub-agent should not have created this' >&2\nexit 1\n",
	})
	initRepo(t, proj)

	// The delegated work: a file created in the shared tree, by Bash so that it
	// actually lands.
	subScript := filepath.Join(t.TempDir(), "sub.sh")
	writeScenario(t, subScript, harness.Turns("sub done",
		Bash("sb1", "echo 'delegated work' > from-sub.md"),
	))

	res := e.Run(proj, "s-013-06", "delegate some work", Turns("root done",
		Dispatch("d1", "do the job", subScript, ""),
	))

	// The rule ran at the sub-agent's own cycle. Without this the rest is a test
	// about a conversation in which nothing ever objected.
	if _, err := os.Stat(ranLog); err != nil {
		t.Fatalf("the guardrail never ran at the sub-agent's stop, so nothing here is about a "+
			"sub-agent's refusal:\n%s", res.Output)
	}

	// The file the sub-agent made is really there. A refusal is an objection to
	// work that landed, not an undo — and if the Bash never executed, the rule
	// above fired on something else.
	if !e.Exists(proj, "from-sub.md") {
		t.Fatalf("the sub-agent's own work never reached the tree, so the refusal was not about "+
			"delegated work:\n%s", res.Output)
	}

	// THE CLAIM. The refusal reached the conversation, with its own words.
	//
	// Read from the blocking attachments rather than from the output as a whole:
	// a guardrail's folder path travels on every hook payload, so searching the
	// transcript for the rule's name finds it whether or not any refusal ever
	// named it — an assertion that cannot fail.
	blocking := e.BlockingErrorsFrom(proj, "s-013-06", "SubagentStop")
	if len(blocking) == 0 {
		t.Fatalf("a guardrail refused the sub-agent's work and nothing recorded it at the "+
			"sub-agent's own cycle — the refusal reached nothing, which is exactly the hole "+
			"this test exists to close:\n%s", res.Output)
	}
	told := strings.Join(blocking, "\n")
	if !strings.Contains(told, "the sub-agent should not have created this") {
		t.Errorf("the refusal was recorded but not its words, so nothing can act on it:\n%s", told)
	}
	if !strings.Contains(told, "nosubwork") {
		t.Errorf("the refusal did not name the guardrail that produced it:\n%s", told)
	}
}

// T013_07: the other side of it — a sub-agent whose work nothing objects to
// leaves no refusal on the record.
//
// Without this, T013_06 passes against an engine or a harness that reports every
// delegated cycle as refused, which would be worse than reporting none: a
// consumer could no longer tell the two apart, and the block loop would re-run
// every sub-agent until the cap.
func TestT013_07_AnUnobjectionableSubagentCycleRecordsNoRefusal(t *testing.T) {
	e := New(t)
	proj := e.Project()

	// A rule that runs on the same event and allows. Present rather than absent,
	// so what is exercised is a guardrail REACHING a verdict of "fine" — with no
	// rule at all, the dispatch would have nothing bound and the cycle would
	// prove only that nothing ran.
	ranLog := filepath.Join(t.TempDir(), "allowed.log")
	e.Guardrail(proj, "permitsubwork", refuseCreatedFiles, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> " + ranLog + "\nexit 0\n",
	})
	initRepo(t, proj)

	subScript := filepath.Join(t.TempDir(), "sub.sh")
	writeScenario(t, subScript, harness.Turns("sub done",
		Bash("sb1", "echo 'delegated work' > from-sub.md"),
	))

	res := e.Run(proj, "s-013-07", "delegate some work", Turns("root done",
		Dispatch("d1", "do the job", subScript, ""),
	))

	if _, err := os.Stat(ranLog); err != nil {
		t.Fatalf("the permitting guardrail never ran, so this proves nothing about a cycle that "+
			"was judged and allowed:\n%s", res.Output)
	}
	if blocking := e.BlockingErrorsFrom(proj, "s-013-07", "SubagentStop"); len(blocking) > 0 {
		t.Fatalf("a sub-agent cycle nothing objected to was recorded as refused (%v) — a refusal "+
			"that appears without one stops meaning anything:\n%s", blocking, res.Output)
	}
	if !res.Saw("root done") {
		t.Errorf("the dispatching session did not complete though nothing refused:\n%s", res.Output)
	}
}

// refuseCreatedFiles binds a rule to every file created, after the fact — the
// event a sub-agent's own Post dispatch raises for the work it did.
const refuseCreatedFiles = `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh
---

# Objects to every file created, after the fact
`
