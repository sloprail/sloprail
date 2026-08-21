package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// A payload that names only a session id now RESOLVES (PR #19 review item).
//
// cite grounds a quote in the END USER's own words, and those words live in the
// ROOT session's transcript — an AskUserQuestion answer is recorded there, a typed
// message is recorded there. So resolving the root is exactly what cite needs, and
// a session id (whether it arrives on a hook payload's session_id or in the
// CLAUDE_CODE_SESSION_ID environment variable) names precisely that. An earlier
// version of cite REFUSED this case, on the theory that a session id "names the
// root even when a sub-agent is asking" — but resolving the root is the goal, not a
// hazard, and a sub-agent's OWN trajectory is caught from the resolved FILE
// (IsSubagentTranscript) rather than guessed from the environment. So the fail-
// closed refusal is gone; these tests pin that the session-id resolution now works.

// T029_13: a hook-shaped payload naming ONLY a session id (and the cwd needed to
// locate its project dir) resolves to <config>/projects/<encoded-cwd>/<session-id>.jsonl
// — the ROOT's own file, written by the mock — and cite grounds the quote there,
// exit 0. This is the case the old code failed closed on; it now resolves, because
// the root is where the user's words are.
func TestT029_13_PayloadSessionIDResolvesTheRoot(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// A real session, its transcript written by the mock under the config dir, with
	// the citable phrase in the prompt (the user's own words) on line 1.
	sessionID := "s-029-13"
	e.Run(proj, sessionID, "please tidy the ORPHANED helper now", Turns("done",
		Bash("b1", "echo ok > ok.md"),
	))
	path := e.TranscriptPath(proj, sessionID)

	// The payload names only the session id and the cwd — no transcript_path, no
	// agent fields. record() joins <config>/projects/<encoded-cwd>/<session-id>.jsonl,
	// so CLAUDE_CONFIG_DIR must point at the config dir the mock wrote to (runBin
	// otherwise defaults it to the sandbox HOME's own .claude).
	payload := `{"session_id":"` + sessionID + `","cwd":"` + proj + `"}`
	env := []string{"CLAUDE_CONFIG_DIR=" + e.ConfigDir()}
	res := e.CLIDirectStdinEnv(proj, payload, env, "sr-session", "trajectory", "cite", "ORPHANED helper")
	if res.Code != 0 {
		t.Fatalf("cite from a session-id payload exited %d, want 0 (it resolves the root):\n%s", res.Code, res.Output)
	}
	want := fmt.Sprintf("%s:1", path)
	if strings.TrimSpace(res.Output) != want {
		t.Fatalf("stdout = %q, want %q (the line the prompt sits on in the root transcript)",
			strings.TrimSpace(res.Output), want)
	}
}

// T029_14: the SAME quote and the SAME session also resolve with an explicit --path
// — exit 0. Kept as the companion to T029_13: whether the root is reached by a
// session-id resolution or by --path, cite mints the same citation, because the
// trajectory is the end user's either way. (The remaining refusal is about a
// sub-agent's trajectory, exercised in test_029_09_subagent_test.go, and is read
// from the file, not from how the path was resolved.)
func TestT029_14_ExplicitPathToSameRootStillResolves(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-029-14", "please tidy the ORPHANED helper now", Turns("done",
		Bash("b1", "echo ok > ok.md"),
	))
	path := e.TranscriptPath(proj, "s-029-14")

	res := cite(e, proj, path, "ORPHANED helper")
	if res.Code != 0 {
		t.Fatalf("cite with an explicit --path to the root exited %d, want 0:\n%s", res.Code, res.Output)
	}
	if !strings.Contains(res.Output, path+":") {
		t.Fatalf("cite with --path did not print the citation:\n%s", res.Output)
	}
}
