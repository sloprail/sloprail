package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sloprail/sloprail/internal/procgroup"
	"github.com/sloprail/sloprail/internal/scriptexec"
)

// This file runs the two kinds of executable a check names — a `script`/`prepare`
// and a `judge`'s substrate — and turns each into a Verdict, fail-closed.
//
// The mechanics mirror services/sr-session's old runHooks, deliberately: a check
// is a declared script (a path plus plain arguments, exec'd directly, no shell; the judge
// substrate is a shell line) run from the guard's own folder with the payload on stdin, under a per-check timeout, in its own process
// group so a wedged child (a model call, most of all) can be killed as a group.
// Everything that is not a clean exit is a refusal — the mechanism failing must
// not read as approval.

// defaultCheckTimeout bounds how long one script/prepare/judge may take before
// it is killed and read as a refusal, when the check names no timeout of its
// own.
//
// Was 30s (the old dispatch's own bound for one hook), on the reasoning that
// it "sits under the harness's own deadline": whichever bound fires first
// decides what the user sees, and only this one can name the rule. That
// reasoning was sound but the harness's actual deadline was never measured —
// Claude Code's own hook timeout is 600s (confirmed against the hooks
// reference), twenty times this bound. Under real load (a nested sr-agent
// judge call competing with an already-running session, several checks
// queued in one turn) 30s was measured to time out repeatedly, refusing a
// clean write for a reason that has nothing to do with its content — the
// exact "a check that could not run must refuse" failure mode this bound
// exists to produce ON PURPOSE for a genuinely wedged check, firing instead
// on an ordinary one that was simply slow.
//
// Now 600s — Claude Code's own per-hook default. 60s (itself a guess, 2x the
// 30s that failed under contention) still refused ordinary slow judges in
// real sessions, and every such refusal is a guardrail misfiring on a reason
// unrelated to the change it was judging. The per-check bound now exists only
// to catch a genuinely wedged check, not to pace a slow one.
//
// A THIRD deadline sits between this one and Claude Code's 600s: one
// PreToolUse (or Stop) invocation of `sr-session` can run SEVERAL checks in
// sequence — the structure gate, every context's enter, every gate, every
// file-guard — each individually bounded by this constant, so the
// whole invocation's wall-clock is not this constant alone. That outer,
// per-hook-INVOCATION bound is Claude Code's own `timeout` field on a
// hooks.json entry (marketplace/plugins/sloprail/hooks/hooks.json sets it to
// 3600s explicitly, rather than trust either side's default) — measured the
// hard way: a10n-claude-mock, the e2e test double for Claude Code, defaults an
// unset hook's timeout to 60s (services/claude-mock/internal/hooks/
// invoker.go's defaultHookTimeout, in the sibling a10n-cli repo). Briefly
// raising THIS constant to 90s, above that 60s, with hooks.json still silent
// on `timeout`, meant the mock killed the outer hook process before this
// constant's own deadline could ever fire — the exact "harness kills the
// engine, which renders no verdict" failure mode this file's own package doc
// warns about, causing a dangerous FAIL-OPEN in e2e
// (tests/e2e/harness/pre_tool/019_hook_failure_surface). hooks.json's explicit 3600s
// fixes the mock/Claude-Code layering regardless of this constant's own
// value (real Claude Code would otherwise wait the full 600s default per
// hook, and the mock's 60s default no longer applies once a value is
// declared). This constant MUST stay under hooks.json's bound: a harness that
// kills the hook renders no verdict, which is fail-OPEN, whereas this bound
// firing is a refusal. 600s leaves room for several slow judges in one
// invocation before the 3600s outer bound is reached.
//
// A judge check may OVERRIDE this with its own `timeout` (dot-dir-file-store/
// main.tsp Check.timeout) — a hard invariant-checking rubric can legitimately
// take longer than a quick one, so the bound is per-judge. runShell takes the
// resolved timeout as a parameter and falls back to this when it is zero; a
// script/prepare has no `timeout` field and always runs under this default.
//
// SLOPRAIL_CHECK_TIMEOUT overrides this for the process's own lifetime — unset
// in any production path, and read only here. It exists so an e2e test that
// deliberately wedges a check to prove the timeout mechanism itself (rather
// than any check's own logic) does not have to actually wait out 600 real
// seconds per assertion: tests/e2e/harness/pre_tool/019_hook_failure_surface sets it
// low via the harness before launching the mock, so the SAME code path this
// constant governs in production is exercised end to end in a few seconds
// instead of minutes. Parsed once at package init — a malformed or absent
// value silently keeps the 600s default rather than failing the process that
// happens to read it first.
var defaultCheckTimeout = func() time.Duration {
	if v := os.Getenv("SLOPRAIL_CHECK_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 600 * time.Second
}()

// checkKillGrace caps how long Wait may block after the process group is killed.
// SIGKILL cannot be caught, so this is only reached by a descendant wedged in an
// uninterruptible syscall — without it one such process restores the unbounded
// hang this timeout exists to remove.
const checkKillGrace = 2 * time.Second

// killAfterTerm is how long a timed-out check's group has to exit on SIGTERM before SIGKILL: longer
// than the grace sr-agent gives its own model call, so that one is not orphaned by the kill.
const killAfterTerm = procgroup.Grace + time.Second

// exitNotExecutable and exitNotFound are the statuses a shell uses to say it
// could not run the command at all, as opposed to the command running and
// failing. Named so the refusal reason can diagnose them.
const (
	exitNotExecutable = 126
	exitNotFound      = 127
)

// maxSignalNumber bounds the 128+signum exit-code convention a shell uses to
// report a signalled child: an exit code in (128, 128+maxSignalNumber] names the
// signal that killed the work. 64 spans the POSIX signals and Linux real-time
// signals, without a platform-specific SIGRTMAX (which darwin does not define).
const maxSignalNumber = 64

// scriptCall is one script/prepare execution: the guard's folder, the script's
// path (relative to it), the payload for stdin, and the session facts a script
// needs in its environment (the guard's name, the workspace, the session id, the
// transcript path).
type scriptCall struct {
	Dir       string
	Script    string
	Stdin     []byte
	GuardName string

	// Workspace, SessionID and TranscriptPath are the session facts a check may
	// need beyond the payload: SR_WORKSPACE to resolve a project-relative path (a
	// goal's verify.sh under <workspace>/goal/…) and to key its own `sr-session
	// state`, SR_SESSION_ID for that same keying, SR_TRANSCRIPT for a check that
	// reads the trajectory itself. They mirror what the old-format hook env sets
	// (services/sr-session hookScope.env), so a new-format check reaches its
	// workspace and state the same way an old-format hook does. Empty values are
	// left unset (the same "unset is diagnosable" stance), except Workspace, which
	// the caller must supply resolved.
	Workspace      string
	SessionID      string
	TranscriptPath string

	// AgentID is the sub-agent the hook fired inside, as the harness reported it
	// (Claude Code's agent_id), emitted as SR_AGENT_ID. Empty in the main session.
	// Unlike the session facts above it is always set, empty included: a check
	// that spawns an agent hands its environment down, and an inherited sub-agent
	// id would tell that agent's own hooks they run inside a sub-agent they are
	// not. A rule refusing work inside sub-agents tests `[ -n "$SR_AGENT_ID" ]`.
	AgentID string

	// LaunchedBy is the colon-separated list of guards whose checks are on the
	// current call stack, emitted as SLOPRAIL_LAUNCHED_BY so a check that spawns
	// sr-agent marks provenance and the dispatch one level down declines to
	// re-fire THIS guard on its own launched agent's writes (the re-entry guard).
	//
	// The CALLER computes it — appendLaunchedBy(os.Getenv, guardName) in
	// services/sr-session, which appends this guard to any value inherited from an
	// outer launched check — because this package cannot import that one (the
	// append+dedup logic and the SLOPRAIL_LAUNCHED_BY constant are the source of
	// truth in services/sr-session/provenance.go). env() only forwards it. Empty
	// leaves the variable unset, which is correct for a check that cannot spawn an
	// agent and harmless for one whose own name is the only entry.
	LaunchedBy string

	// Env is extra environment appended last (after the engine's own), so it wins:
	// a changeset's SR_TREE, SR_BASE and SR_HEAD.
	Env []string
}

// scriptResult is what a script/prepare execution produced.
//
// Passed is the verdict (exit zero). Reason is what to tell the agent on a
// non-pass — the script's own words where it gave any, a diagnosis otherwise,
// never empty when !Passed. Stdout is the raw standard output, kept for a prepare
// whose stdout carries the additionalContext object.
type scriptResult struct {
	Passed bool
	Reason string
	Stdout []byte

	// Code is the exit status of a script that ran to completion; -1 when it
	// could not be started or was killed. Read only where an exit code other
	// than zero carries meaning of its own (a prerequisite's `when`).
	Code int

	// Unrunnable is set when the script could not be run at all (its file is not
	// executable or has no shebang, it could not be started, it was killed on the
	// timeout), as against one that ran and declined with a non-zero exit. A caller
	// where "declined" would permit something (a context's enter) must refuse on it.
	Unrunnable bool

	// Errored is set when the script ran and said, in its structured stdout ({"reason": "...",
	// "error": true}), that it could not do its job (a tool it needs failed): a refusal, but no
	// verdict on the content, so it is never cached.
	Errored bool

	// Cause is, for an Unrunnable result, the diagnosis alone (the file and what is wrong with it,
	// no framing sentence about a check or an action), for a caller that words its own refusal.
	Cause string
}

// runScriptExec is the production runScript: it execs the declared script directly
// (a path plus plain arguments, scriptexec.Argv: never `sh -c`, so there is no shell
// syntax to leave unchecked) from the guard's folder, with the payload on stdin, and
// reports pass/fail by exit code.
//
// FAIL-CLOSED throughout. A clean exit passes; every other outcome — a non-zero
// exit, a timeout, a process that would not start, a NUL in the command — refuses.
// A caller could read the exit status itself, but the point of running it here is
// that all the ways a check can fail land on the safe side without each caller
// arranging it.
//
// The relative-to-Dir resolution matches the spec's "resolved relative to the
// guard's folder": an author writes `./verify.sh` and it runs from the guard's
// directory, so a relative path finds the sibling script.
// sr:invariant checks/exit-status-is-the-verdict
// sr:invariant checks/check-that-cannot-answer-refuses
func runScriptExec(s scriptCall) (scriptResult, error) {
	// A script/prepare has no per-check timeout — its runtime is the author's to
	// bound (spec: model/timeout are judge-only) — so it always runs under the
	// default. Passing 0 would work too (runShell falls back), but naming the
	// default here keeps the expired message's duration honest.
	if err := scriptexec.VerifyDeclared(s.Dir, s.Script); err != nil {
		return scriptResult{
			Cause:      err.Error(),
			Passed:     false,
			Reason:     fmt.Sprintf("the check %q could not be run: %v. The action was refused because a check that cannot run must not be read as approval.", s.Script, err),
			Code:       -1,
			Unrunnable: true,
		}, nil
	}
	argv, err := scriptexec.Argv(s.Dir, s.Script)
	if err != nil {
		return scriptResult{
			Cause:      err.Error(),
			Passed:     false,
			Reason:     fmt.Sprintf("the check %q could not be run: %v. The action was refused because a check that cannot run must not be read as approval.", s.Script, err),
			Code:       -1,
			Unrunnable: true,
		}, nil
	}
	stdout, stderr, code, expired, signal, startErr := runArgv(s.Dir, argv, s.Stdin, s.env(), defaultCheckTimeout)
	if startErr != nil {
		if errors.Is(startErr, fs.ErrNotExist) {
			// The declared script is not there (renamed, or the wrong name): say so plainly,
			// the one fact the author needs to find it.
			return scriptResult{
				Cause:  fmt.Sprintf("it was not found: %v", startErr),
				Passed: false,
				Reason: fmt.Sprintf(
					"the check %q was not found: %v. The action was refused because a check that cannot run must not be read as approval.",
					s.Script, startErr),
				Code:       -1,
				Unrunnable: true,
			}, nil
		}
		// Could not be started at all — a NUL byte in the command, a Dir that went
		// away. Not the rule's decision, but a mechanism failure, and a mechanism
		// failure refuses (fail-closed) rather than erroring up to a caller who
		// would then have to decide again. The old dispatch returned an error here
		// and refused at the call site; folding it into a refusal keeps every
		// script outcome one shape.
		return scriptResult{
			Cause:  startErr.Error(),
			Passed: false,
			Reason: fmt.Sprintf(
				"the check %q could not be run: %v. The action was refused because a check that cannot run must not be read as approval.",
				s.Script, startErr),
			Code:       -1,
			Unrunnable: true,
		}, nil
	}
	if expired {
		return scriptResult{
			Cause:  fmt.Sprintf("it was killed after %s without answering", defaultCheckTimeout),
			Passed: false,
			Reason: fmt.Sprintf(
				"the check %q was killed after %s without answering, and the action was refused because a check that did not answer must not be read as approval.%s",
				s.Script, defaultCheckTimeout, quoted(stderr)),
			Code:       -1,
			Unrunnable: true,
		}, nil
	}
	if code == 0 {
		return scriptResult{Passed: true, Stdout: stdout}, nil
	}
	return scriptResult{
		Passed:  false,
		Reason:  scriptRefusalReason(s.Script, code, signal, stdout, stderr),
		Stdout:  stdout,
		Code:    code,
		Errored: structuredError(stdout),
	}, nil
}

// env is the environment one check runs in: the parent's, plus the guard's own
// name under SR_GUARDRAIL so a check calling `sr-session state` reaches its own
// keyspace, plus SR_GUARDRAIL_DIR so a script can find its siblings by absolute
// path if it needs to.
//
// The session/workspace/transcript variables the old hooks set are NOT added
// here: this runner is called from inside a hook that already resolved those and
// set them on ITS OWN environment (see the caller in services/sr-session), and
// the payload on stdin carries transcriptPath for a check that reads the record.
// Adding them again would duplicate a resolution this runner does not own.
// sr:invariant checks/check-launched-agent-does-not-reenter-its-rule
func (s scriptCall) env() []string {
	env := os.Environ()
	if s.GuardName != "" {
		env = append(env, "SR_GUARDRAIL="+s.GuardName)
	}
	if s.Dir != "" {
		// Absolute, per this variable's own documented promise above — measured
		// to matter: s.Dir reached here RELATIVE at least once in practice
		// (traced to a declaration's Dir being built by filepath.Join against a
		// relative base somewhere upstream of the plugin/project resolution
		// chain), and Go's exec.Cmd.Dir silently resolves a relative directory
		// against the PARENT process's own cwd at spawn time — so the spawned
		// child was placed in the right directory by coincidence (the parent
		// happened to already be there), while this env var carried the same
		// relative string verbatim into a process that has no way to know what
		// the parent's cwd was. filepath.Abs is a no-op when s.Dir is already
		// absolute (the ordinary case), so this only changes behavior for the
		// case that was already broken. A resolution failure here (only
		// possible if the process's own cwd cannot be read) falls back to the
		// original string rather than silently dropping the variable — still
		// wrong in that one scenario, but no worse than before this fix.
		dir := s.Dir
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		env = append(env, "SR_GUARDRAIL_DIR="+dir)
	}
	// The session facts, mirroring the old-format hook env so a new-format check
	// reaches its workspace and state the same way. Appended AFTER os.Environ() so
	// the engine's answer wins over any stale value an outer process exported — the
	// same append-ordering the old hookScope.env relies on. Left unset when empty,
	// the documented "unset is diagnosable" stance a rule tests for.
	if s.Workspace != "" {
		env = append(env, "SR_WORKSPACE="+s.Workspace)
	}
	if s.SessionID != "" {
		env = append(env, "SR_SESSION_ID="+s.SessionID)
	}
	if s.TranscriptPath != "" {
		env = append(env, "SR_TRANSCRIPT="+s.TranscriptPath)
	}
	// sr:invariant subagents/subagent-id-reaches-checks
	env = append(env, "SR_AGENT_ID="+s.AgentID)
	env = append(env, s.Env...)
	// The re-entry provenance: which guards' checks are on this call stack. Set so
	// a check that spawns sr-agent carries it across the exec into the launched
	// agent's own hooks, where the dispatch reads it and declines to re-fire those
	// guards on the agent's writes. The caller already appended THIS guard (and
	// deduped) via appendLaunchedBy; this only forwards the computed value.
	// Appended AFTER os.Environ() so the engine's answer wins over any stale outer
	// value — the same append-ordering the other vars use and the old
	// hookScope.env relies on. Empty is left unset (a check that cannot launch an
	// agent needs no provenance). The variable name mirrors provenance.go's
	// LaunchedByEnv, which is the source of truth for it.
	if s.LaunchedBy != "" {
		env = append(env, launchedByEnv+"="+s.LaunchedBy)
	}
	return env
}

// launchedByEnv is the environment variable naming which guards' checks are on
// the current call stack — the re-entry provenance a check that spawns sr-agent
// must carry so the launched agent's own hooks decline to re-fire those guards.
//
// A LITERAL copy of services/sr-session/provenance.go's LaunchedByEnv, which is
// the source of truth: this package sits BELOW services/sr-session in the
// dependency graph and cannot import it, and the value is computed there
// (appendLaunchedBy) and threaded in as scriptCall.LaunchedBy / judgeCall.LaunchedBy.
// If the name ever changes, it changes in provenance.go and here together.
const launchedByEnv = "SLOPRAIL_LAUNCHED_BY"

// runShell runs one shell command from dir, with stdin, under the given timeout,
// in its own process group, and reports what happened.
//
// The single primitive both a script and the judge substrate go through. It
// returns the two streams, the exit code, whether the deadline expired, the
// signal that killed the process (0 when it exited on its own), and a start error
// (the process could not be launched at all) — leaving the fail-closed
// interpretation to the caller, which differs slightly between a script (exit code
// is the verdict) and a judge (a verify script inside sr-agent decides).
//
// The signal is returned separately because exitErr.ExitCode() FLATTENS a
// signalled death to -1, losing which signal it was — so a check the OS killed
// (a segfault, an outer `kill`, an OOM) is indistinguishable from any other
// non-clean exit by the code alone. Recovering the signal here lets the caller's
// refusal reason say the check was KILLED rather than report a bare "exit -1, no
// reason" (which the old dispatch did not). It is 0 for a clean or ordinary
// non-zero exit, and set only when WaitStatus.Signaled(). A TIMEOUT is also a
// signalled death (the process-group SIGKILL), but expired is reported first and
// the caller special-cases it before ever consulting the signal, so the timeout
// keeps its own "did not answer in time" wording.
//
// The timeout is a PER-RUN parameter rather than the const it once was, so a
// judge check can carry its own (dot-dir-file-store/main.tsp Check.timeout). A
// zero or negative value falls back to defaultCheckTimeout — the script path and
// any judge without an override run under the same defaultCheckTimeout. Whatever bound
// applies, an expiry is still a refusal at the call site: fail-closed is
// preserved at the per-check bound exactly as it was at the const one.
//
// The process GROUP is killed on timeout, not just the shell: a check that spawns
// sr-agent, which spawns a model call, leaves children that outlive a kill aimed
// at the shell and hold the pipes open — so Setpgid gives the shell its own group
// and one signal to the negated pgid ends the tree. This is the old runHooks'
// mechanism, unchanged, because the failure it prevents (a leaked model-calling
// subprocess per guarded action, and an unbounded hang) is identical here.
func runShell(dir, command string, stdin []byte, env []string, timeout time.Duration) (stdout, stderr []byte, code int, expired bool, signal syscall.Signal, startErr error) {
	return runArgv(dir, []string{"sh", "-c", command}, stdin, env, timeout)
}

// runArgv is runShell for an argv exec'd directly: the one primitive, so a script (no shell)
// and the judge substrate (a shell line) share the timeout, the process-group kill and the
// exit/signal reading.
func runArgv(dir string, argv []string, stdin []byte, env []string, timeout time.Duration) (stdout, stderr []byte, code int, expired bool, signal syscall.Signal, startErr error) {
	if timeout <= 0 {
		timeout = defaultCheckTimeout
	}
	var outBuf, errBuf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Dir = dir
	c.Env = env
	c.Stdin = bytes.NewReader(stdin)
	c.Stdout = &outBuf
	c.Stderr = &errBuf
	var termTimer *time.Timer
	c.Cancel = func() error {
		// SIGTERM first, so a child that keeps children of its own in groups of their own
		// (sr-agent's model call) can take them down with it; SIGKILL for what is left.
		pgid := c.Process.Pid
		if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		termTimer = time.AfterFunc(killAfterTerm, func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
		return nil
	}
	c.WaitDelay = checkKillGrace

	// Run, with the group registered while it runs: a signal to this process then reaches it.
	err := procgroup.Run(c, true)
	if termTimer != nil && termTimer.Stop() && c.Process != nil {
		// The leader is gone but the SIGTERM'd group may not be: finish it now rather than leave
		// a timer to fire at a group id that may have been reused.
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	expired = ctx.Err() != nil
	stdout, stderr = outBuf.Bytes(), errBuf.Bytes()

	if err == nil {
		return stdout, stderr, 0, false, 0, nil
	}
	if expired {
		// The deadline fired. Report it as expired regardless of the exit shape;
		// the caller refuses on it before reading the code or the signal, which for
		// a signalled death (the process-group SIGKILL) would otherwise read as a
		// bare "killed" and lose the "did not answer in time" wording.
		return stdout, stderr, -1, true, 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// A non-clean exit that was not the deadline. Recover the signal if the OS
		// killed the process (a crash, an outer `kill`, an OOM): ExitCode() reports
		// -1 for a signalled death and drops which signal it was, so the caller
		// could otherwise only say "exit -1, no reason". WaitStatus.Signal() gives
		// the actual signal; it is left 0 for an ordinary non-zero exit.
		//
		// TWO shapes, because a signal can reach the direct child OR a shell's own
		// grandchild:
		//  1. THE SHELL ITSELF was signalled (`Signaled()` true) — macOS `sh -c`
		//     execs the single command, so `kill -9 $$` kills the process Wait
		//     watches. Read the signal straight off the wait status.
		//  2. THE SHELL EXITED CLEANLY reporting a signalled child (code >= 128) —
		//     Linux `sh -c "./x"` FORKS `./x`, so `kill -9 $$` in the script kills
		//     the FORKED child; the outer shell then exits NORMALLY with 128+signum
		//     (the POSIX convention). `Signaled()` is false, but the exit code still
		//     names the signal that killed the work. Without this the caller falls
		//     back to the shell's bare "Killed" stderr and loses the signal number —
		//     exactly the macOS/Linux split T036 catches.
		code := exitErr.ExitCode()
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			signal = ws.Signal()
		} else if code > 128 && code <= 128+maxSignalNumber {
			// The 128+signum convention: the low bits above 128 are the signal that
			// killed the work. 64 covers POSIX signals plus Linux real-time signals
			// without depending on a platform-specific SIGRTMAX (absent on darwin).
			signal = syscall.Signal(code - 128)
		}
		return stdout, stderr, code, false, signal, nil
	}
	// Not an ExitError: the process could not be started at all.
	return stdout, stderr, -1, false, 0, err
}

// scriptRefusalReason works out what to tell the agent about a script that exited
// non-zero. There is always something to say — this never returns "".
//
// The script's own words win where it gave any (stdout first — a script that
// writes there is answering; then stderr — `echo … >&2; exit 1` is an ordinary
// refusal idiom). A process the OS KILLED gets a "killed by signal" diagnosis
// rather than the bare "exit -1" the code alone would yield (see below). The two
// could-not-run statuses get a diagnosis naming the fix, since the shell's own
// message is accurate and useless. Failing everything, the exit status itself is
// reported. This is a trimmed form of the old dispatch's refusalReason, which had
// been tuned against real hooks.
//
// signal is the signal that killed the process (0 when it exited on its own),
// recovered by runShell because ExitCode() flattens a signalled death to -1. A
// TIMEOUT is also a signalled death, but runScriptExec/askJudge report expiry
// FIRST and never reach this with a timeout, so the signal branch here only ever
// describes a NON-timeout kill (a crash, an outer `kill`, an OOM) — the old
// dispatch called that "killed", and this restores that word in place of the
// regressed "exit -1 … no reason".
func scriptRefusalReason(script string, code int, signal syscall.Signal, stdout, stderr []byte) string {
	// A structured {"reason": "..."} on stdout is the script speaking; prefer it
	// even over a signal — a script that managed to author a verdict said something
	// the agent should hear.
	if reason := structuredReason(stdout); reason != "" {
		return reason
	}

	// The OS killed the process (not the timeout — that was reported before this).
	// Say so, in the "killed" family the timeout message uses, rather than letting
	// the -1 sentinel fall through to "exit -1 … no reason". Any dying words on
	// stderr are quoted, the same as the timeout and could-not-run paths.
	if signal != 0 {
		return fmt.Sprintf(
			"the check %q was killed by signal %d (%s), and the action was refused because a check the OS killed must not be read as approval.%s",
			script, int(signal), signal, quoted(stderr))
	}

	switch code {
	case exitNotExecutable:
		return fmt.Sprintf(
			"the check %q could not be run: it is not executable (chmod +x it, and check its interpreter line). "+
				"The action was refused because a check that cannot run must not be read as approval.%s",
			script, quoted(stderr))
	case exitNotFound:
		return fmt.Sprintf(
			"the check %q was not found. The command is resolved relative to the rule's own folder. "+
				"The action was refused because a check that cannot run must not be read as approval.%s",
			script, quoted(stderr))
	}

	if text := plainText(stdout); text != "" {
		return text
	}
	if text := plainText(stderr); text != "" {
		return text
	}
	return fmt.Sprintf("the check %q refused (exit %d) but gave no reason", script, code)
}

// structuredReason reads a {"reason": "..."} object off a script's stdout, the
// same shape the old hooks emit for a structured verdict. Empty when stdout is
// not that shape or carries no reason.
func structuredReason(stdout []byte) string {
	var res struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &res); err == nil {
		return strings.TrimSpace(res.Reason)
	}
	return ""
}

// structuredError reads the {"error": true} flag off a script's stdout: the script ran but could
// not reach a verdict.
// sr:invariant checks/error-reports-are-not-answers
func structuredError(stdout []byte) bool {
	var res struct {
		Error bool `json:"error"`
	}
	return json.Unmarshal(bytes.TrimSpace(stdout), &res) == nil && res.Error
}

// plainText returns trimmed output meant to be read by a person, or "" when there
// is nothing usable. JSON is excluded: it is either a structured verdict already
// handled or a detail the agent should not be shown raw.
func plainText(b []byte) string {
	text := strings.TrimSpace(string(b))
	if text == "" || json.Valid([]byte(text)) {
		return ""
	}
	return text
}

// quoted appends what the shell said, when it said anything, so the underlying
// message is not lost behind a diagnosis.
func quoted(stderr []byte) string {
	text := strings.TrimSpace(string(stderr))
	if text == "" {
		return ""
	}
	return " (" + text + ")"
}

// resolveScriptPath is the absolute path of a script named relative to the guard
// folder, for a helper that needs it outside the script's working directory. Kept small and
// separate because the judge path builds a prompt naming files and wants the
// absolute form.
func resolveScriptPath(dir, script string) string {
	if filepath.IsAbs(script) {
		return script
	}
	return filepath.Join(dir, script)
}
