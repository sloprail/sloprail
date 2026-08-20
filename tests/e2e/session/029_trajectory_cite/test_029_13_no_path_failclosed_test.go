package e2e

import (
	"strings"
	"testing"
)

// The no-path fail-closed guard (PR comment on session_trajectory_cite.go, D2).
//
// cite is an ordinary TOOL CALL an agent makes mid-work, and a tool call's process
// environment carries NO signal that it is running inside a sub-agent rather than
// the root. That is grounded in the Claude Code docs, not guessed: the fields that
// name a sub-agent (agent_id / agent_type) arrive only on a HOOK's JSON stdin, never
// as environment variables to a tool call [code.claude.com/docs/en/hooks.md]; and no
// CLAUDE_SESSION_ID is exported to a tool call — the session id reaches a script only
// as the `session_id` hook field, with transcripts stored at
// ~/.claude/projects/<project>/<session-id>.jsonl [code.claude.com/docs/en/sessions.md].
//
// So when cite has NO --path and the only thing that could resolve the current
// trajectory is a session id, that resolution names the ROOT even when a sub-agent
// is asking — the reviewer's "it'll always resolve to the root." cite cannot rule
// out a sub-agent context, so it FAILS CLOSED with exit 3 rather than risk citing
// the parent's dispatch as the user's own words. An explicit --path is unaffected:
// it names a concrete file whose own record settles whether it is a sub-agent's.

// T029_13: cite with no --path, resolvable ONLY from a session id, fails closed with
// exit 3 — even though the quote is genuinely present in the resolved (root)
// transcript. The refusal is on stderr and nothing citable is on stdout; the harm it
// prevents is minting a citation cite cannot prove is the end user's, because a
// session id names the root whether or not a sub-agent is the caller.
func TestT029_13_NoPathSessionIDOnlyFailsClosed(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// A real session, its transcript written by the mock under the config dir, with
	// the citable phrase in the prompt (the user's own words).
	e.Run(proj, "s-029-13", "please tidy the ORPHANED helper now", Turns("done",
		Bash("b1", "echo ok > ok.md"),
	))

	// A payload naming ONLY a session id (and the cwd needed to locate its project
	// dir) — no transcript_path, no agent fields. record() resolves this to
	// <project>/<session-id>.jsonl, the ROOT's own file. cite must refuse rather than
	// cite it, because nothing here can exclude a sub-agent caller.
	payload := `{"session_id":"s-029-13","cwd":"` + proj + `"}`
	res := e.CLIDirectStdin(proj, payload, "sr-session", "trajectory", "cite", "ORPHANED helper")
	if res.Code != 3 {
		t.Fatalf("cite resolvable only from a session id exited %d, want 3 (fail-closed):\n%s", res.Code, res.Output)
	}
	// It refused; it did not cite. Nothing on stdout naming the transcript.
	if strings.Contains(res.Output, "s-029-13.jsonl:") {
		t.Fatalf("cite printed a citation, must fail closed instead:\n%s", res.Output)
	}
	// And the refusal says why: a possible sub-agent context that --path would resolve.
	if !res.Saw("--path") || !res.Saw("sub-agent") {
		t.Fatalf("the fail-closed refusal did not explain itself (want it to name --path and the sub-agent hazard):\n%s", res.Output)
	}
}

// T029_14: the SAME quote and the SAME session resolve normally with an explicit
// --path — exit 0. This is the contrast that proves the fail-closed is about the
// unruleable RESOLUTION (session id alone), not about the trajectory or the quote:
// pointed at the very same root transcript by --path, whose own record shows it is
// not a sub-agent's, cite mints the citation it refused to guess at above.
func TestT029_14_ExplicitPathToSameRootStillResolves(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-029-14", "please tidy the ORPHANED helper now", Turns("done",
		Bash("b1", "echo ok > ok.md"),
	))
	path := e.TranscriptPath(proj, "s-029-14")

	// Explicit --path: the fail-closed no-path branch is never reached, and the
	// path-based guard reads the file itself — a root, so it resolves.
	res := cite(e, proj, path, "ORPHANED helper")
	if res.Code != 0 {
		t.Fatalf("cite with an explicit --path to the root exited %d, want 0:\n%s", res.Code, res.Output)
	}
	if !strings.Contains(res.Output, path+":") {
		t.Fatalf("cite with --path did not print the citation:\n%s", res.Output)
	}
}
