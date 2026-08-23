package e2e

import (
	"fmt"
	"strings"
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

// residueReason is the gate's own refusal wording (verify-no-residue.sh) — the
// words that must reach the agent when a message is unaccounted for.
const residueReason = "These user messages are not mapped to any task, and none was marked skip"

// T036_01: a session whose one user message maps to no task and is not skipped is
// REFUSED at Stop, and the gate's own reason reaches the agent.
//
// The whole point of the guardrail: unprocessed intake (a user message that
// became neither a task nor an explicit skip) must not pass silently.
func TestT036_01_UnaccountedMessageRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install example")

	sess := "s-036-01"
	// The agent does some work but never records a task for the request and never
	// skips it. The one user message (the prompt) is left unaccounted for.
	res := e.Run(proj, sess, "please handle request A", Turns("done",
		Say("m1", "I looked at it but recorded nothing."),
	))

	if !res.Refused() && len(e.BlockingErrorsFrom(proj, sess, "Stop")) == 0 {
		t.Fatalf("the intake gate did not refuse a session with an unaccounted user message:\n%s", res.Output)
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, residueReason) {
		t.Errorf("the gate's residue refusal reason did not reach the agent:\n%s", joined)
	}
	// The refusal must name this gate, so the agent can attribute it.
	if !strings.Contains(joined, "verify-intake-complete") {
		t.Errorf("the refusal did not name the gate:\n%s", joined)
	}
	// And it must carry the offending message's ref (the transcript path + line
	// range), which is how the agent knows WHICH message to account for. The prompt
	// does not sit on line 1 — the mock opens the transcript with its preamble block —
	// so the ref line is the root message's actual physical line.
	msgRef := fmt.Sprintf(":%d-%d", e.RootMessageLine(sess), e.RootMessageLine(sess))
	if !strings.Contains(joined, msgRef) {
		t.Errorf("the refusal did not carry the unaccounted message's ref (%s):\n%s", msgRef, joined)
	}
}

// T036_02: the gate ADMITS once the message IS accounted for by a task — the
// control that proves T036_01's refusal is CONDITIONAL, not a gate that blocks
// every Stop.
//
// The gate collects the message refs, subtracts those a task file references, and
// refuses only on what is left. This drives the subtract-to-empty path: a task
// file under tasks/ at the repository root references the one user message's ref,
// so the residue empties and the gate admits. The gate's grep is anchored on
// $SR_WORKSPACE, so the repo-root tasks/ (where a user following the example puts
// it) is exactly where the gate looks.
func TestT036_02_AccountedMessageAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)

	sess := "s-036-02"
	// The message ref names the root prompt's ACTUAL physical line — the mock opens the
	// transcript with its preamble block ahead of the root, so it is not line 1.
	ref := fmt.Sprintf("%s:%d-%d", e.TranscriptPath(proj, sess), e.RootMessageLine(sess), e.RootMessageLine(sess))
	// A task file at the repository root, referencing the message ref in the
	// parenthesized markdown-link form the gate matches: (/abs/path:N-N).
	e.WriteFile(proj, "tasks/task-a/ASK.md",
		"# Task A\n\nRaised by the user request ("+ref+").\n")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install + task")

	res := e.Run(proj, sess, "please handle request A", Turns("done",
		Say("m1", "Recorded it as task-a."),
	))

	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("the gate refused a session whose only user message WAS mapped to a task:\n%v", blocks)
	}
	if res.Refused() {
		t.Errorf("the gate refused an accounted-for session:\n%s", res.Output)
	}
}

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
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install + task")

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
