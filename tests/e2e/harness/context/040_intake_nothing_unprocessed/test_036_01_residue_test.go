package e2e

import (
	"fmt"
	"testing"
)

// This file drives the intake-nothing-unprocessed gate end to end against the
// SHIPPED example. The gate fires on Stop and refuses when a user message this
// session is neither mapped to a task under tasks/ nor explicitly skipped.
//
// # What the mock can and cannot present
//
// The gate counts user messages via `sr-session trajectory normalize | jq
// 'select(.type=="user")'`. Every scenario here drives a single `Say` turn, which is a
// pure-text assistant record with NO tool call — so the mock synthesises no
// tool_result, and the only `type:"user"` entry the transcript carries is the seeded
// root (the prompt). (A tool turn WOULD add a tool_result `type:"user"` entry — since
// the mock now stamps a uuid on it, transcript reading keeps it — so these scenarios
// deliberately avoid one, keeping exactly one accountable user message.) That one
// message is enough to exercise all three logic branches (unaccounted → refuse;
// accounted via a task → admit; not-fired-outside-Stop), which is what these tests do.
// A residue of SEVERAL distinct user messages cannot be presented through this mock,
// and is noted as a harness limitation rather than faked.
//
// The root prompt does NOT sit on physical line 1: the mock opens every fresh
// transcript with its no-uuid preamble block (custom-title / mode / last-prompt) ahead
// of the root, so the message's ref line is the harness's RootMessageLine, not 1.

// T036_03: the gate does NOT fire on a mid-turn event — only on Stop.
//
// The control for the trigger: the example binds the check to `Stop` alone. A
// file write mid-session must not itself invoke the residue check (that check is
// an end-of-turn accounting, not a per-write gate). Here the write lands with no
// refusal at the moment it happens; any refusal that appears is the Stop cycle's,
// not the write's. This proves the gate is a Stop gate, not a file gate.
func TestT036_03_DoesNotFireOnFileWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	// Account for the one message so the Stop cycle itself admits, isolating the
	// question "did the WRITE trigger a refusal" from "did Stop refuse".
	sess := "s-036-03"
	ref := fmt.Sprintf("%s:%d-%d", e.TranscriptPath(proj, sess), e.RootMessageLine(sess), e.RootMessageLine(sess))
	e.WriteFile(proj, "tasks/t/ASK.md", "("+ref+")\n")
	e.CommitAll(proj, "install + task")

	res := e.Run(proj, sess, "handle request A", Turns("done",
		Write("w1", "notes/scratch.md", "some mid-turn note"),
	))

	// The write itself must not have been blocked (a Stop gate does not deny a
	// PreToolUse). The file lands.
	if res.Refused() {
		t.Errorf("a Stop-only gate blocked a mid-turn file write:\n%s", res.Output)
	}
	if !e.Exists(proj, "notes/scratch.md") {
		t.Errorf("the mid-turn write did not land, so a gate wrongly intercepted it")
	}
}
