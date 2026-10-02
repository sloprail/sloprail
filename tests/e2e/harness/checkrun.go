package harness

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
)

// File-guards are judged by `sr check run --base --head`, not by the Stop hook. The harness
// stands in for the caller that states the range: after every Run it records what a session
// produced, so the refusals the Stop hook used to deliver are still there to assert on.
//
//   - the base is HEAD as it was when the session's first Run began (RunBase), so the range
//     is what the session committed, accumulating over repeated Runs;
//   - after each Run the harness runs `sr check run --base <RunBase> --head HEAD` and keeps
//     its refusals under the session; BlockingErrors / BlockingErrorsFrom(..., "Stop") return
//     them after the gates' and contexts' own Stop refusals, so tests that asserted a
//     file-guard's refusal at Stop read the same on either side;
//   - NoAutoCheck() turns that off for a test that states its own range.

// emptyTree is git's empty tree: the base of a session that began before the first commit.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// NoAutoCheck stops the harness from running `sr check run` after each Run.
func NoAutoCheck() Option { return func(e *Env) { e.noAutoCheck = true } }

// RunBase is HEAD as it was before the session's first Run (the empty tree for a project
// with no commit yet), "" for a session this Env never ran.
func (e *Env) RunBase(sessionID string) string { return e.runBase[sessionID] }

// noteRunBase records the base of a session at its first Run.
func (e *Env) noteRunBase(projDir, sessionID string) {
	if _, ok := e.runBase[sessionID]; ok {
		return
	}
	base := emptyTree
	if out, err := exec.Command("git", "-C", projDir, "rev-parse", "--verify", "-q", "HEAD").Output(); err == nil {
		if sha := strings.TrimSpace(string(out)); sha != "" {
			base = sha
		}
	}
	e.runBase[sessionID] = base
}

// afterRun is the automatic `sr check run` of the range the session has produced so far.
func (e *Env) afterRun(projDir, sessionID string) {
	e.t.Helper()
	if e.noAutoCheck {
		return
	}
	base := e.runBase[sessionID]
	if base == "" {
		return
	}
	if err := exec.Command("git", "-C", projDir, "rev-parse", "--verify", "-q", "HEAD").Run(); err != nil {
		return // nothing committed: nothing to judge
	}
	if refusals := e.CheckRunRange(projDir, sessionID, base, "HEAD"); len(refusals) > 0 {
		e.checkHistory[sessionID] = append(e.checkHistory[sessionID], refusals...)
	}
}

// checkCmd runs `sr check <verb> --base --head` in projDir as the session, returning
// stdout (the refusals) apart from the combined output.
func (e *Env) checkCmd(projDir, sessionID, verb, base, head string) (stdout string, res Result) {
	e.t.Helper()
	cmd := exec.Command(filepath.Join(e.binDir, "sr"), "check", verb, "--base", base, "--head", head)
	cmd.Dir = projDir
	cmd.Env = append(HostEnv(), "HOME="+e.home, "SLOP_SUBBIN_DIR="+e.binDir)
	cmd.Env = append(cmd.Env, e.hookEnv(sessionID)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		e.t.Fatalf("harness: sr check %s: %v\n%s", verb, err, errb.String())
	}
	e.t.Logf("sr check %s --base %s --head %s (exit %d):\n%s%s", verb, base, head, code, out.String(), errb.String())
	return out.String(), Result{Output: out.String() + errb.String(), Code: code}
}

// CheckRunRaw is `sr check run --base --head` as the session: exit code and output.
func (e *Env) CheckRunRaw(projDir, sessionID, base, head string) Result {
	e.t.Helper()
	_, res := e.checkCmd(projDir, sessionID, "run", base, head)
	return res
}

// CheckVerify is `sr check verify --base --head` as the session (read-only; a non-zero
// exit is unsatisfied).
func (e *Env) CheckVerify(projDir, sessionID, base, head string) Result {
	e.t.Helper()
	_, res := e.checkCmd(projDir, sessionID, "verify", base, head)
	return res
}

// CheckRunRange runs `sr check run` over an explicit range and returns its refusals: the
// text of the refusal (every rule that refused, as one block, the form a Stop blocked with),
// or nil when nothing refused.
func (e *Env) CheckRunRange(projDir, sessionID, base, head string) []string {
	e.t.Helper()
	stdout, res := e.checkCmd(projDir, sessionID, "run", base, head)
	if res.Code == 0 || strings.TrimSpace(stdout) == "" {
		return nil
	}
	return []string{strings.TrimSpace(stdout)}
}

// CheckRun is CheckRunRange over the session's RunBase..HEAD.
func (e *Env) CheckRun(projDir, sessionID string) []string {
	e.t.Helper()
	return e.CheckRunRange(projDir, sessionID, e.RunBase(sessionID), "HEAD")
}

// FileGuardRefusals is every refusal the automatic `sr check run` recorded for a session,
// de-duplicated, in order: BlockingErrorsFrom(..., "Stop") without the gates'.
func (e *Env) FileGuardRefusals(projDir, sessionID string) []string {
	e.t.Helper()
	return dedupeStrings(e.checkHistory[sessionID])
}

func dedupeStrings(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
