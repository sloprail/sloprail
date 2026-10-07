package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The environment fallback (PR #19 review item). cite is agent-facing — an
// ordinary tool call the agent, or a guardrail's own script, makes mid-work — and
// the common case is `cite "<quote>"` with NO --path and NO piped hook payload.
// There is no payload to name a transcript, so cite must AUTO-DETECT "the
// trajectory we are running in right now" from the environment: Claude Code
// exports CLAUDE_CODE_SESSION_ID naming the current session, and the transcript
// lives at <config>/projects/<encoded-cwd>/<session-id>.jsonl. Resolving THAT file
// (the root's, which holds the citations — an AskUserQuestion answer is recorded in
// the root's transcript) is sufficient, so --path is optional.
//
// This is the case an earlier version of cite refused (it claimed no session id
// reached a tool call, which is false — the variable is CLAUDE_CODE_SESSION_ID and
// it IS set). These tests prove the direct agent call now resolves and grounds the
// quote.

// T029_15: the agent runs `sr-session trajectory cite "<quote>"` as a plain CLI —
// no --path, no payload on stdin — inside a session whose transcript the mock
// produced. cite resolves the CURRENT session's own transcript from
// CLAUDE_CODE_SESSION_ID + the working directory and grounds the quote to its line,
// exit 0.
func TestT029_15_NoPathResolvesCurrentSessionFromEnv(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// A real session: the mock writes its transcript under the config dir, with the
	// citable phrase in the prompt (the user's own words). The prompt does not sit on
	// physical line 1 — the mock opens the transcript with its no-uuid preamble block
	// (custom-title / mode / last-prompt) ahead of the root — so the expected line is
	// derived from the file the mock wrote rather than hardcoded.
	sessionID := "s-029-15"
	e.Run(proj, sessionID, "please refactor the auth module carefully", Turns("done",
		Bash("b1", "echo on-it > note.md"),
	))
	path := e.TranscriptPath(proj, sessionID)
	promptLine := physicalLine(t, path, "please refactor the auth module carefully")

	// The agent-facing call: cwd is the project (so os.Getwd() names the tree
	// Claude Code filed the transcript under), CLAUDE_CODE_SESSION_ID names the
	// session, CLAUDE_CONFIG_DIR points at the config dir the mock wrote to. NO
	// --path, NO stdin.
	env := e.SessionEnv(sessionID)
	res := e.CLIDirectEnv(proj, env, "sr-session", "trajectory", "cite", "auth module")
	if res.Code != 0 {
		t.Fatalf("cite with no --path (env-resolved) exited %d, want 0:\n%s", res.Code, res.Output)
	}
	want := fmt.Sprintf("%s:%d", path, promptLine)
	if strings.TrimSpace(res.Output) != want {
		t.Fatalf("stdout = %q, want %q (the line the prompt sits on in the env-resolved transcript)",
			strings.TrimSpace(res.Output), want)
	}
}

// T029_16: the env fallback grounds an AskUserQuestion ANSWER, the very thing cite
// exists to cite and the reason resolving the ROOT session is sufficient — the
// answer envelope is recorded in the root's own transcript. Same no-path,
// no-payload agent call as T029_15; here the quote is from the selected answer.
func TestT029_16_NoPathResolvesAnAnswerFromEnv(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	sessionID := "s-029-16"
	// The prompt deliberately does NOT contain the answer substring, so the match is
	// unique to the answer envelope. Its physical line is derived from the file the mock
	// wrote — the answer envelope follows the mock's no-uuid preamble block and the root
	// prompt, so it does not sit on a hardcoded line.
	e.Run(proj, sessionID, "here is the task", Turns("done",
		harness.AnswerIfAsked(t, "q1", [2]string{"which approach?", "go with the second option"})...,
	))
	path := e.TranscriptPath(proj, sessionID)
	answerLine := physicalLine(t, path, "go with the second option")

	env := e.SessionEnv(sessionID)
	// Without an answer record the env-resolved transcript is read all the same, and
	// holds no answer: the quote is not the person's words (exit 1, silent on stdout),
	// while the prompt in that same transcript resolves.
	if !harness.HasCap(t, harness.CapAskUserQuestion) {
		if answerLine != 0 {
			t.Fatalf("the record holds the answer text without an answer record:\n%s", readFile(t, path))
		}
		res := e.CLIDirectEnv(proj, env, "sr-session", "trajectory", "cite", "second option")
		if res.Code != 1 || strings.Contains(res.Output, path+":") {
			t.Fatalf("citing words never said (env-resolved) exited %d, want 1 and nothing on stdout:\n%s", res.Code, res.Output)
		}
		res = e.CLIDirectEnv(proj, env, "sr-session", "trajectory", "cite", "here is the task")
		if res.Code != 0 {
			t.Fatalf("the prompt did not resolve through the env fallback, so the miss above proves nothing (exit %d):\n%s", res.Code, res.Output)
		}
		return
	}
	res := e.CLIDirectEnv(proj, env, "sr-session", "trajectory", "cite", "second option")
	if res.Code != 0 {
		t.Fatalf("citing an answer with no --path (env-resolved) exited %d, want 0:\n%s", res.Code, res.Output)
	}
	want := fmt.Sprintf("%s:%d", path, answerLine)
	if strings.TrimSpace(res.Output) != want {
		t.Fatalf("stdout = %q, want %q", strings.TrimSpace(res.Output), want)
	}
}

// T029_17: with NO --path, NO payload, and NO CLAUDE_CODE_SESSION_ID in the
// environment, there is genuinely nothing to resolve — cite refuses with the
// no-trajectory error (a clear refusal on stderr), NOT a silent wrong answer. This
// is the honest floor of the fallback: it resolves when it can, and says so plainly
// when it cannot.
func TestT029_17_NoPathNoEnvRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// A real transcript exists, but nothing tells cite which session it is in: no
	// --path, empty stdin, and CLAUDE_CODE_SESSION_ID unset for this invocation.
	e.Run(proj, "s-029-17", "please tidy the ORPHANED helper now", Turns("done",
		Bash("b1", "echo ok > ok.md"),
	))

	// CLAUDE_CONFIG_DIR is set; CLAUDE_CODE_SESSION_ID is explicitly EMPTY so the
	// fallback has no session id to derive from. Set to empty rather than merely
	// omitted because a developer runs this suite inside a real Claude Code session
	// whose own CLAUDE_CODE_SESSION_ID is on os.Environ() — the empty assignment
	// wins over that inherited value, so the test means the same thing locally and
	// in CI (where the variable is absent).
	env := []string{
		"CLAUDE_CONFIG_DIR=" + e.ConfigDir(),
		"CLAUDE_CODE_SESSION_ID=",
	}
	res := e.CLIDirectEnv(proj, env, "sr-session", "trajectory", "cite", "ORPHANED helper")
	if res.Code == 0 {
		t.Fatalf("cite with no --path, no payload and no session id exited 0, want non-zero:\n%s", res.Output)
	}
	if !res.Saw("no trajectory to read") {
		t.Fatalf("the refusal did not say why:\n%s", res.Output)
	}
	// It refused; it did not cite. Nothing on stdout naming the transcript.
	if strings.Contains(res.Output, "s-029-17.jsonl:") {
		t.Fatalf("cite printed a citation, must refuse instead:\n%s", res.Output)
	}
}
