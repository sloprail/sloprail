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
//     Real Claude Code records a refusal as a "Stop hook feedback" turn — what
//     the re-run agent reads — followed by a hook_blocking_error attachment
//     carrying its text, and for a SubagentStop it writes both into the
//     SUB-AGENT's own transcript (<session>/subagents/agent-<id>.jsonl), never
//     the dispatching session's. The mock wrote nothing but a line on its own
//     stderr, so from outside, a refused delegated cycle and a clean one were
//     the same conversation. It now writes both where real Claude Code does,
//     read with harness.SubagentBlockingErrors.
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
// and its words reach the sub-agent — recorded in the sub-agent's own
// transcript as the feedback turn it is re-run with, and the SubagentStop
// attachment beside it.
//
// The sub-agent takes its own worktree and commits there: a file-guard judges
// commits, and only an agent that owns a tree is judged at its own stop (a
// sub-agent sharing the dispatching session's tree is judged at the root's). The
// mock does not APPLY a sub-agent's Write, so the delegated work is done with
// Bash, which the mock executes inside the worktree.
//
// Asserted against the sub-agent's own record specifically, not against every
// refusal anywhere. Sharing the tree means the root's own Stop sees from-sub.md
// too and refuses it a second time, in the ROOT's record, so "some refusal
// arrived" is true even when the sub-agent's cycle judged nothing — measured,
// by re-stubbing subagent-stop and watching the loose version of this test stay
// green.
func TestT013_06_ASubagentRefusalReachesTheConversation(t *testing.T) {
	e := New(t)
	proj := e.Project()

	ranLog := filepath.Join(t.TempDir(), "refused.log")
	e.FileGuard(proj, "nosubwork", refuseCreatedFiles, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> " + ranLog + "\n" +
			"echo '{\"reason\":\"the sub-agent should not have created this\"}'\nexit 1\n",
	})
	e.GitInit(proj)

	// The delegated work: a file created in the shared tree, by Bash so that it
	// actually lands.
	// SubagentScript ends with the judging a real sub-agent does before it stops.
	subScript := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo 'delegated work' > from-sub.md"),
	).ThenCommit("the sub-agent's work"))

	res := e.Run(proj, "s-013-06", "delegate some work", Turns("root done",
		Dispatch("d1", "do the job", subScript, harness.OwnTree(t)),
	))

	// The rule ran at the sub-agent's own cycle. Without this the rest is a test
	// about a conversation in which nothing ever objected.
	if _, err := os.Stat(ranLog); err != nil {
		t.Fatalf("the guardrail never ran at the sub-agent's stop, so nothing here is about a "+
			"sub-agent's refusal:\n%s", res.Output)
	}

	// On a harness whose sub-agents cannot take a tree of their own (Codex, Cursor) the
	// sub-agent works in the root's tree and is judged at the ROOT's Stop, not at its
	// own: nothing refuses the sub-agent's stop, and the refusal, with its words and
	// the guardrail's name, reaches the root instead.
	if !harness.HasCap(t, harness.CapWorktrees) {
		if _, err := os.Stat(filepath.Join(proj, "from-sub.md")); err != nil {
			t.Fatalf("the sub-agent's work never reached the shared tree, so the refusal was not "+
				"about delegated work:\n%s", res.Output)
		}
		if !e.NoSubagentStopBlock(proj, "s-013-06") {
			t.Fatalf("a sub-agent sharing the root's tree was refused at its own stop, though only "+
				"the agent that owns the tree is judged:\n%s", res.Output)
		}
		told := strings.Join(e.BlockingErrorsFrom(proj, "s-013-06", "Stop"), "\n")
		if !strings.Contains(told, "the sub-agent should not have created this") {
			t.Fatalf("the root's Stop did not refuse the sub-agent's committed work with the rule's words:\n%s", told)
		}
		if !strings.Contains(told, "nosubwork") {
			t.Fatalf("the root's refusal did not name the guardrail that produced it:\n%s", told)
		}
		return
	}

	// The file the sub-agent made is really there. A refusal is an objection to
	// work that landed, not an undo — and if the Bash never executed, the rule
	// above fired on something else.
	if !inSomeWorktree(t, proj, "from-sub.md") {
		t.Fatalf("the sub-agent's own work never reached its worktree, so the refusal was not "+
			"about delegated work:\n%s", res.Output)
	}

	// THE CLAIM. The refusal reached the sub-agent, with its own words: in the
	// sub-agent's own record, as the feedback turn it is re-run with followed
	// by the SubagentStop attachment (SubagentBlockingErrors requires both).
	//
	// Read from the blocking attachments rather than from the output as a whole:
	// a guardrail's folder path travels on every hook payload, so searching the
	// transcript for the rule's name finds it whether or not any refusal ever
	// named it — an assertion that cannot fail.
	if leaked := e.BlockingErrorsFrom(proj, "s-013-06", "SubagentStop"); len(leaked) != 0 {
		t.Errorf("a SubagentStop refusal was recorded in the dispatching session's record (%v); "+
			"it belongs in the sub-agent's own", leaked)
	}
	blocking := e.SubagentBlockingErrors(proj, "s-013-06")
	if len(blocking) == 0 {
		t.Fatalf("a guardrail refused the sub-agent's work and nothing recorded it at the "+
			"sub-agent's own cycle, in its own record — the refusal reached nothing, which is exactly the hole "+
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
	e.FileGuard(proj, "permitsubwork", refuseCreatedFiles, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> " + ranLog + "\nexit 0\n",
	})
	e.GitInit(proj)

	// SubagentScript ends with the judging a real sub-agent does before it stops.
	subScript := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo 'delegated work' > from-sub.md"),
	).ThenCommit("the sub-agent's work"))

	res := e.Run(proj, "s-013-07", "delegate some work", Turns("root done",
		Dispatch("d1", "do the job", subScript, harness.OwnTree(t)),
	))

	if _, err := os.Stat(ranLog); err != nil {
		t.Fatalf("the permitting guardrail never ran, so this proves nothing about a cycle that "+
			"was judged and allowed:\n%s", res.Output)
	}
	// (Where the sub-agent shares the root's tree, the permitting rule ran at the root's
	// Stop and the sub-agent's stop was not judged at all; the assertion is the same.)
	if !e.NoSubagentStopBlock(proj, "s-013-07") {
		t.Fatalf("a sub-agent cycle nothing objected to was recorded as refused — a refusal "+
			"that appears without one stops meaning anything:\n%s", res.Output)
	}
	if !res.Saw("root done") {
		t.Errorf("the dispatching session did not complete though nothing refused:\n%s", res.Output)
	}
}

// refuseCreatedFiles is a file-guard that refuses every `.md` file the sub-agent
// committed. A sub-agent's own cycle ends at its SubagentStop, where the file-guard
// judges what it committed; a refusal blocks that stop, read with
// e.SubagentBlockingErrors from the sub-agent's own record. `match: "**/*.md"`
// selects the delegated `.md` work the sub-agent's Bash creates.
const refuseCreatedFiles = `match: "**/*.md"
checks:
  - script: ./refuse.sh
`

// inSomeWorktree reports whether a file exists in any worktree bound under the
// project's .claude/worktrees — where an isolated sub-agent's work lands.
func inSomeWorktree(t *testing.T, proj, rel string) bool {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(proj, ".claude", "worktrees"))
	if err != nil {
		return false
	}
	for _, en := range entries {
		if _, err := os.Stat(filepath.Join(proj, ".claude", "worktrees", en.Name(), rel)); err == nil {
			return true
		}
	}
	return false
}
