package harness

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/sloprail/sloprail/internal/checkcache"
)

// File-guards are judged by `sr-checks run --base --head`, not by the Stop hook. The harness
// stands in for the caller that states the range: after every Run it records what a session
// produced, so the refusals the Stop hook used to deliver are still there to assert on.
//
//   - the base is HEAD as it was when the session's first Run began (RunBase), so the range
//     is what the session committed, accumulating over repeated Runs;
//   - after each Run the harness runs `sr-checks run --base <RunBase> --head HEAD` and keeps
//     its refusals under the session; BlockingErrors / BlockingErrorsFrom(..., "Stop") return
//     them after the gates' and contexts' own Stop refusals, so tests that asserted a
//     file-guard's refusal at Stop read the same on either side;
//   - NoAutoCheck() turns that off for a test that states its own range.

// emptyTree is git's empty tree: the base of a session that began before the first commit.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// KeepOrigin leaves origin/main where GitInit put it: the harness does not move it up to the
// pre-session HEAD at a project's first session. For a test whose subject is where a range
// starts (the folder's own start, commits unpushed before the session): origin's position must
// not be what excludes or includes them.
func KeepOrigin() Option { return func(e *Env) { e.keepOrigin = true } }

// NoAutoCheck stops the harness from running `sr-checks run` after each Run.
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
	e.publishPreSession(projDir, base)
}

// publishPreSession moves origin/main (the local remote-tracking ref) up to where the project
// stood before the project's first session: what a test committed before it ran is what a real
// project already has pushed, so the range `--base origin/main` is only the session's work.
// Once per project; only when origin/main is an ancestor of that point.
func (e *Env) publishPreSession(projDir, base string) {
	if e.keepOrigin || e.origins[projDir] == "" || base == emptyTree || e.published[projDir] {
		return
	}
	e.published[projDir] = true
	if exec.Command("git", "-C", projDir, "merge-base", "--is-ancestor", "origin/main", base).Run() != nil {
		return
	}
	Git(e.t, projDir, "update-ref", "refs/remotes/origin/main", base)
}

// withPreStopRun appends, to a scenario, the turn a real agent takes before it ends its turn: it
// asks the checks to judge what it committed (`sr-checks run` over the range its folder tracks:
// the merge base with origin's default branch up to HEAD). The Stop then VERIFIES that range
// against the stored results — it never calls a model — so what a test sees refused at Stop is
// what the judges said, with the same texts as before. NoAutoCheck() leaves a scenario as written.
func (e *Env) withPreStopRun(projDir, sessionID string, s Scenario) Scenario {
	if e.noAutoCheck || !slices.Contains(e.driver.Caps(), CapStopHooks) {
		// A harness whose stop hook never fires has nothing to verify the range against stored
		// results at its end, and its mock models no compound command line.
		return s
	}
	base := e.stopBase(projDir, sessionID)
	e.preStopRuns++
	rootRun := e.driver.ShellEnv() + "sr-checks run --base " + shQuote(base) + " --head HEAD >/dev/null 2>&1"
	turn := Bash("srprestop-"+strconv.Itoa(e.preStopRuns), e.driver.SessionExport(sessionID)+"cd "+shQuote(projDir)+" && "+rootRun+"; "+runTrackedRanges(false))
	out := s
	out.turns = append(append([]Turn{}, s.turns...), turn)
	return out
}

// runTrackedRanges is the shell a real agent runs before it stops: `sr-checks run` over EVERY range
// the session tracks (`sr-session refs list --json`: folder, head, base), in the range's own
// folder — sub-agent worktrees, other repositories, branches it left — exactly as `refs list`
// prints them (a branch that is gone is run at the commit it last pointed at, a folder that is gone is
// skipped with a line saying so). The ranges are read when the step runs, since the commits they cover do not exist
// when the scenario is written. The run carries the session id, as a real agent's Bash does, so a
// folder outside the session's tree still resolves the session. A sub-agent (own=true) runs only the ranges of its own worktree.
// A refusal is the judges' answer (the Stop reports it); anything else that goes wrong —
// `refs list` failing, a folder or range `run` cannot use (nothing on stdout, a non-zero exit) — is
// printed and fails the turn, never swallowed.
func runTrackedRanges(own bool) string {
	filter := `select(.UntrackedReason=="")`
	if own {
		filter += ` | select(.Folder==$top)`
	}
	return `top=$(git rev-parse --show-toplevel 2>/dev/null); ` +
		`refs=$(sr-session refs list --json 2>&1) || { echo "harness: sr-session refs list failed: $refs" >&2; exit 1; }; ` +
		`rows=$(printf '%s' "$refs" | jq -r --arg top "$top" '.[]? | ` + filter + ` | [.Folder,.Head,.Base,.HeadSHA] | @tsv') || { echo "harness: sr-session refs list printed no ranges: $refs" >&2; exit 1; }; ` +
		`printf '%s\n' "$rows" | { rc=0; while IFS="	" read -r f h b sha; do ` +
		`[ -n "$f" ] || continue; ` +
		`[ -d "$f" ] || { echo "harness: skipping $f (the folder is gone)" >&2; continue; }; ` +
		`(cd "$f" && git rev-parse --verify -q "$h^{commit}" >/dev/null) || h="$sha"; ` +
		`out=$(cd "$f" && ` + mustDriver().ShellEnv() + `sr-checks run --base "$b" --head "$h" 2>&1); st=$?; ` +
		`if [ "$st" -ne 0 ] && [ -z "$out" ]; then echo "harness: sr-checks run --base $b --head $h in $f failed ($st) with no output" >&2; rc=1; ` +
		`elif [ "$st" -ne 0 ] && ! printf '%s' "$out" | grep -q "file-guard"; then echo "harness: sr-checks run --base $b --head $h in $f failed ($st): $out" >&2; rc=1; fi; ` +
		`done; exit $rc; }`
}

// stopBase is the base of the range a Stop verifies: the merge base with origin's default
// branch (a project made with GitInit has one), else where the session began.
func (e *Env) stopBase(projDir, sessionID string) string {
	if e.origins[projDir] != "" {
		return "origin/main"
	}
	return e.runBase[sessionID]
}

// StopJudged is StopNow after the turn a real agent takes first: `sr-checks run` over the
// range the Stop will verify. A test that drives the Stop itself, over commits it made by hand,
// uses it where it wants the judges to have been asked.
func (e *Env) StopJudged(projDir, sessionID string, active bool) Result {
	e.t.Helper()
	e.CheckRunRaw(projDir, sessionID, e.stopBase(projDir, sessionID), "HEAD")
	return e.StopNow(projDir, sessionID, active)
}

// checkCmd runs `sr-checks <verb> --base --head` in projDir as the session, returning
// stdout (the refusals) apart from the combined output.
func (e *Env) checkCmd(projDir, sessionID, verb, base, head string) (stdout string, res Result) {
	e.t.Helper()
	cmd := e.checkExec(projDir, sessionID, verb, base, head)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		e.t.Fatalf("harness: sr-checks %s: %v\n%s", verb, err, errb.String())
	}
	e.t.Logf("sr-checks %s --base %s --head %s (exit %d):\n%s%s", verb, base, head, code, out.String(), errb.String())
	return out.String(), Result{Output: out.String() + errb.String(), Code: code}
}

// checkExec is the `sr-checks <verb> --base --head` command, built and not started.
func (e *Env) checkExec(projDir, sessionID, verb, base, head string) *exec.Cmd {
	e.ensureTranscript(projDir, sessionID)
	cmd := exec.Command(filepath.Join(e.binDir, "sr"), "checks", verb, "--base", base, "--head", head)
	cmd.Dir = projDir
	cmd.Env = append(HostEnv(), "HOME="+e.home, "SLOP_SUBBIN_DIR="+e.binDir)
	cmd.Env = append(cmd.Env, e.hookEnv(sessionID)...)
	return cmd
}

// CheckRunCmd is `sr-checks run --base --head` as the session, built and not started, for a
// test that must interrupt a run part-way (it owns the process: start it, kill it, wait for it).
func (e *Env) CheckRunCmd(projDir, sessionID, base, head string) *exec.Cmd {
	e.t.Helper()
	return e.checkExec(projDir, sessionID, "run", base, head)
}

// CheckRunRaw is `sr-checks run --base --head` as the session: exit code and output.
func (e *Env) CheckRunRaw(projDir, sessionID, base, head string) Result {
	e.t.Helper()
	_, res := e.checkCmd(projDir, sessionID, "run", base, head)
	return res
}

// CheckVerify is `sr-checks verify --base --head` as the session (read-only; a non-zero
// exit is unsatisfied).
func (e *Env) CheckVerify(projDir, sessionID, base, head string) Result {
	e.t.Helper()
	_, res := e.checkCmd(projDir, sessionID, "verify", base, head)
	return res
}

// CheckRunRange runs `sr-checks run` over an explicit range and returns its refusals: the
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

// FileGuardRefusals is every refusal the Stop delivered for a session, de-duplicated, in
// order. File-guards are judged by `sr-checks run` (the pre-Stop turn) and the Stop VERIFIES
// the stored results, so a file-guard's refusal reaches the agent as a Stop refusal.
func (e *Env) FileGuardRefusals(projDir, sessionID string) []string {
	e.t.Helper()
	return e.BlockingErrorsFrom(projDir, sessionID, "Stop")
}

// addOrigin gives a project a LOCAL BARE origin (in a temporary directory removed with the
// test): the initial commit is pushed to it and origin/HEAD points at main, so `--base origin/main`
// works, `sr-checks run` pushes the results branch sloprail/checks there, and a second clone sees
// them. Done once, by GitInit.
func (e *Env) addOrigin(dir string) {
	e.t.Helper()
	bare := filepath.Join(e.t.TempDir(), "origin.git")
	Git(e.t, filepath.Dir(bare), "init", "-q", "--bare", "--initial-branch=main", bare)
	// no detached auto-gc after a push: it races the TempDir cleanup ("directory not empty").
	Git(e.t, bare, "config", "gc.auto", "0")
	Git(e.t, bare, "config", "receive.autogc", "false")
	Git(e.t, bare, "config", "maintenance.auto", "false")
	Git(e.t, dir, "config", "gc.auto", "0")
	Git(e.t, dir, "config", "maintenance.auto", "false")
	Git(e.t, dir, "remote", "add", "origin", bare)
	Git(e.t, dir, "push", "-q", "origin", "main")
	Git(e.t, dir, "fetch", "-q", "origin")
	Git(e.t, dir, "remote", "set-head", "origin", "main")
	e.origins[dir] = bare
}

// Origin is the path of a project's local bare origin ("" for a directory GitInit never made).
func (e *Env) Origin(projDir string) string { return e.origins[projDir] }

// CloneFresh is a second clone of a project's origin in a new temporary directory — "another
// machine": it has only what was pushed.
func (e *Env) CloneFresh(projDir string) string {
	e.t.Helper()
	origin := e.origins[projDir]
	if origin == "" {
		e.t.Fatalf("harness: %s has no origin: it was not made with GitInit", projDir)
	}
	dir := filepath.Join(e.t.TempDir(), "clone")
	Git(e.t, filepath.Dir(dir), "clone", "-q", origin, dir)
	Git(e.t, dir, "config", "user.email", "e2e@example.invalid")
	Git(e.t, dir, "config", "user.name", "E2E")
	Git(e.t, dir, "config", "commit.gpgsign", "false")
	return dir
}

// PushBranch pushes a branch of the project to its origin.
func (e *Env) PushBranch(projDir, branch string) {
	e.t.Helper()
	Git(e.t, projDir, "push", "-q", "origin", branch)
}

// CacheRecords reads back every run the results branch (sloprail/checks) holds, as a10n-shaped
// runs with their checks and items, newest first: what a test asserts a `sr-checks run` stored.
// With an origin it is origin's branch that is read, so a push that never happened shows as empty.
func (e *Env) CacheRecords(projDir string) []checkcache.Run {
	e.t.Helper()
	opt := checkcache.Options{Dir: projDir}
	if e.origins[projDir] != "" {
		opt.Remote = "origin"
	}
	store, err := checkcache.Open(opt)
	if err != nil {
		e.t.Fatalf("harness: open the results branch: %v", err)
	}
	if err := store.Sync(); err != nil {
		e.t.Fatalf("harness: fetch the results branch: %v", err)
	}
	runs, err := store.Runs()
	if err != nil {
		e.t.Fatalf("harness: read the results branch: %v", err)
	}
	return runs
}

// JudgeTracked is the turn a real agent takes before it stops, run by hand: `sr-checks run`
// over every range the session tracks, from dir (a sub-agent's worktree judges its own ranges,
// the root's folder all of them). For a test that commits outside a Run and then drives the
// Stop itself.
func (e *Env) JudgeTracked(dir, sessionID string, subagent bool) {
	e.t.Helper()
	cmd := exec.Command("bash", "-c", runTrackedRanges(subagent))
	cmd.Dir = dir
	cmd.Env = append(HostEnv(), "HOME="+e.home, "PATH="+e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "SLOP_SUBBIN_DIR="+e.binDir)
	cmd.Env = append(cmd.Env, e.hookEnv(sessionID)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("harness: judging the tracked ranges: %v\n%s", err, out)
	}
}

// ensureTranscript gives a session that has not run a turn the record its agent would have: a
// refusal reached without a transcript is stored for no key (any check may read it), so a test
// that judges "as the session" before any turn would otherwise find nothing stored.
func (e *Env) ensureTranscript(projDir, sessionID string) {
	if sessionID == "" {
		return
	}
	e.driver.SeedTranscript(e, projDir, sessionID)
}

// seedTranscriptFile writes record as the session's transcript where the harness keeps it, unless
// one is there.
func (e *Env) seedTranscriptFile(projDir, sessionID, record string) {
	tp := e.transcriptPath(projDir, sessionID)
	if _, err := os.Stat(tp); err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(tp), 0o755); err != nil {
		e.t.Fatalf("harness: %v", err)
	}
	rec := record + "\n"
	if err := os.WriteFile(tp, []byte(rec), 0o644); err != nil {
		e.t.Fatalf("harness: %v", err)
	}
}
