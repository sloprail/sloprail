package harness

import (
	"crypto/sha1"
	"encoding/hex"
	"os/exec"
	"strings"
	"testing"
)

// The agent committing, as a step of a scenario, and the refusal it meets when it
// does not.
//
// commit.go is the test's own hand on the sandbox (CommitAll between runs). These
// are the AGENT's: a file-guard judges commits, so a scenario that writes a file
// and expects the rule to judge it has the agent commit before it stops. One turn
// for that, one way to append it to a scenario, and one assertion for the refusal
// a Stop gives when the commit is missing — never `git add`/`git commit` pasted
// into each test.

// Commit is the turn where the agent commits everything it has written (CommitFile is
// the turn that writes a file and commits it). The id must be unique among the
// session's turns, like any other turn's.
func Commit(id, msg string, trailers ...string) Turn {
	return Bash(id, commitCmd(msg, trailers...))
}

// CommitPaths is a scenario turn in which the AGENT commits only the given paths,
// leaving everything else (an untracked nested clone, say) uncommitted.
func CommitPaths(id, msg string, paths ...string) Turn {
	args := make([]string, len(paths))
	for i, p := range paths {
		args[i] = shQuote(p)
	}
	return Bash(id, "git add -- "+strings.Join(args, " ")+" && git commit -q -m "+shQuote(msg))
}

// ThenCommit is the scenario followed by the agent committing its work: the
// "write, commit, stop" of a test that wants a file-guard to judge what the agent
// wrote. Its id comes from the message and the scenario's own turns, so two runs in
// one session that do different things do not collide (a turn fires once per
// conversation, and a second commit with the same id would be skipped).
func (s Scenario) ThenCommit(msg string, trailers ...string) Scenario {
	h := sha1.New()
	h.Write([]byte(msg + "\x00" + strings.Join(trailers, "\x00")))
	for _, turn := range s.turns {
		h.Write([]byte(turn.jsonl))
	}
	sum := h.Sum(nil)
	s.turns = append(append([]Turn(nil), s.turns...), Commit("commit-"+hex.EncodeToString(sum[:4]), msg, trailers...))
	return s
}

// CommitRequired filters a session's Stop refusals down to the ones that are the
// engine asking for a commit, by its own words.
func CommitRequired(blockingErrors []string) []string {
	var out []string
	for _, m := range blockingErrors {
		if strings.Contains(m, "Commit your work before ending this turn") {
			out = append(out, m)
		}
	}
	return out
}

// AssertCommitRequired fails the test unless the session's Stop was refused for
// uncommitted guarded work, naming every path given. It is the refusal half of a
// refusal-then-pass test; the pass half is the same session's next Stop after the
// agent commits (see ThenCommit), asserted with NoCommitRequired.
func (e *Env) AssertCommitRequired(projDir, sessionID string, paths ...string) {
	e.t.Helper()
	refused := CommitRequired(e.BlockingErrorsFrom(projDir, sessionID, "Stop"))
	if len(refused) == 0 {
		e.t.Fatalf("uncommitted guarded work did not refuse the Stop; blocking errors: %q", e.BlockingErrors(projDir, sessionID))
	}
	for _, p := range paths {
		if !strings.Contains(refused[0], p) {
			e.t.Fatalf("the commit-required refusal should name %q:\n%s", p, refused[0])
		}
	}
}

// NoCommitRequired fails the test if the session's Stop was ever refused for
// uncommitted work. seen is how many such refusals earlier steps already caused
// (zero for a session that never should have met one).
func (e *Env) NoCommitRequired(projDir, sessionID string, seen int) {
	e.t.Helper()
	if n := len(CommitRequired(e.BlockingErrorsFrom(projDir, sessionID, "Stop"))); n != seen {
		e.t.Fatalf("the Stop was refused for uncommitted work (%d refusals, had %d)", n, seen)
	}
}

// CommitInstalled commits everything in dir so a freshly installed guardrail tree is
// part of the session's history rather than a change waiting to be committed: an
// uncommitted rule folder is itself a guarded path (the sloprail plugin's
// authoring-slop selects every hook script), so the first Stop would ask for a commit
// of it. A no-op when dir is not a git repository. A package function, not an Env
// method, for the installers that have no Env — they are handed a directory.
func CommitInstalled(t testing.TB, dir string) {
	t.Helper()
	if err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "--allow-empty", "-m", "install the guardrail tree"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("harness: git %v: %v\n%s", args, err, out)
		}
	}
}
