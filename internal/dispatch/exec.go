package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// This file runs the two kinds of executable a check names — a `script`/`prepare`
// and a `judge`'s substrate — and turns each into a Verdict, fail-closed.
//
// The mechanics mirror services/sr-session's old runHooks, deliberately: a check
// is an arbitrary shell command, so it is run as `sh -c` from the guard's own
// folder with the payload on stdin, under a per-check timeout, in its own process
// group so a wedged child (a model call, most of all) can be killed as a group.
// Everything that is not a clean exit is a refusal — the mechanism failing must
// not read as approval.

// checkTimeout bounds how long one script/prepare/judge may take before it is
// killed and read as a refusal.
//
// Generous because a judge is a model call. It is the same 30s the old dispatch
// uses for one hook, and it sits under the harness's own deadline for the same
// reason: whichever bound fires first decides what the user sees, and only this
// one can name the rule.
const checkTimeout = 30 * time.Second

// checkKillGrace caps how long Wait may block after the process group is killed.
// SIGKILL cannot be caught, so this is only reached by a descendant wedged in an
// uninterruptible syscall — without it one such process restores the unbounded
// hang this timeout exists to remove.
const checkKillGrace = 2 * time.Second

// exitNotExecutable and exitNotFound are the statuses a shell uses to say it
// could not run the command at all, as opposed to the command running and
// failing. Named so the refusal reason can diagnose them.
const (
	exitNotExecutable = 126
	exitNotFound      = 127
)

// scriptCall is one script/prepare execution: the guard's folder, the script's
// path (relative to it), the payload for stdin, and the guard's name for the
// environment.
type scriptCall struct {
	Dir       string
	Script    string
	Stdin     []byte
	GuardName string
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
}

// runScriptExec is the production runScript: it runs the script as `sh -c` from
// the guard's folder, with the payload on stdin, and reports pass/fail by exit
// code.
//
// FAIL-CLOSED throughout. A clean exit passes; every other outcome — a non-zero
// exit, a timeout, a process that would not start, a NUL in the command — refuses.
// A caller could read the exit status itself, but the point of running it here is
// that all the ways a check can fail land on the safe side without each caller
// arranging it.
//
// The `sh -c` shape and the relative-to-Dir resolution match the old hooks and
// the spec's "resolved relative to the guard's folder": an author writes
// `./verify.sh` and it runs from the guard's directory, so a bare relative path
// finds the sibling script.
func runScriptExec(s scriptCall) (scriptResult, error) {
	stdout, stderr, code, expired, startErr := runShell(s.Dir, s.command(), s.Stdin, s.env())
	if startErr != nil {
		// Could not be started at all — a NUL byte in the command, a Dir that went
		// away. Not the rule's decision, but a mechanism failure, and a mechanism
		// failure refuses (fail-closed) rather than erroring up to a caller who
		// would then have to decide again. The old dispatch returned an error here
		// and refused at the call site; folding it into a refusal keeps every
		// script outcome one shape.
		return scriptResult{
			Passed: false,
			Reason: fmt.Sprintf(
				"the check %q could not be run: %v. The action was refused because a check that cannot run must not be read as approval.",
				s.Script, startErr),
		}, nil
	}
	if expired {
		return scriptResult{
			Passed: false,
			Reason: fmt.Sprintf(
				"the check %q was killed after %s without answering, and the action was refused because a check that did not answer must not be read as approval.%s",
				s.Script, checkTimeout, quoted(stderr)),
		}, nil
	}
	if code == 0 {
		return scriptResult{Passed: true, Stdout: stdout}, nil
	}
	return scriptResult{
		Passed: false,
		Reason: scriptRefusalReason(s.Script, code, stdout, stderr),
		Stdout: stdout,
	}, nil
}

// command is the shell line for a script call: the script path as the author
// wrote it, run from the guard's folder so a `./x.sh` resolves there.
func (s scriptCall) command() string { return s.Script }

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
func (s scriptCall) env() []string {
	env := os.Environ()
	if s.GuardName != "" {
		env = append(env, "SR_GUARDRAIL="+s.GuardName)
	}
	if s.Dir != "" {
		env = append(env, "SR_GUARDRAIL_DIR="+s.Dir)
	}
	return env
}

// runShell runs one shell command from dir, with stdin, under the check timeout,
// in its own process group, and reports what happened.
//
// The single primitive both a script and the judge substrate go through. It
// returns the two streams, the exit code, whether the deadline expired, and a
// start error (the process could not be launched at all) — leaving the fail-closed
// interpretation to the caller, which differs slightly between a script (exit code
// is the verdict) and a judge (a verify script inside sr-agent decides).
//
// The process GROUP is killed on timeout, not just the shell: a check that spawns
// sr-agent, which spawns a model call, leaves children that outlive a kill aimed
// at the shell and hold the pipes open — so Setpgid gives the shell its own group
// and one signal to the negated pgid ends the tree. This is the old runHooks'
// mechanism, unchanged, because the failure it prevents (a leaked model-calling
// subprocess per guarded action, and an unbounded hang) is identical here.
func runShell(dir, command string, stdin []byte, env []string) (stdout, stderr []byte, code int, expired bool, startErr error) {
	var outBuf, errBuf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	c := exec.CommandContext(ctx, "sh", "-c", command)
	c.Dir = dir
	c.Env = env
	c.Stdin = bytes.NewReader(stdin)
	c.Stdout = &outBuf
	c.Stderr = &errBuf
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	c.WaitDelay = checkKillGrace

	err := c.Run()
	expired = ctx.Err() != nil
	stdout, stderr = outBuf.Bytes(), errBuf.Bytes()

	if err == nil {
		return stdout, stderr, 0, false, nil
	}
	if expired {
		// The deadline fired. Report it as expired regardless of the exit shape;
		// the caller refuses on it before reading the code, which for a signalled
		// death would otherwise be Go's -1 sentinel.
		return stdout, stderr, -1, true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout, stderr, exitErr.ExitCode(), false, nil
	}
	// Not an ExitError: the process could not be started at all.
	return stdout, stderr, -1, false, err
}

// scriptRefusalReason works out what to tell the agent about a script that exited
// non-zero. There is always something to say — this never returns "".
//
// The script's own words win where it gave any (stdout first — a script that
// writes there is answering; then stderr — `echo … >&2; exit 1` is an ordinary
// refusal idiom). The two could-not-run statuses get a diagnosis naming the fix,
// since the shell's own message is accurate and useless. Failing everything, the
// exit status itself is reported. This is a trimmed form of the old dispatch's
// refusalReason, which had been tuned against real hooks.
func scriptRefusalReason(script string, code int, stdout, stderr []byte) string {
	// A structured {"reason": "..."} on stdout is the script speaking; prefer it.
	if reason := structuredReason(stdout); reason != "" {
		return reason
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
// folder, for a helper that needs it outside the `sh -c` cwd. Kept small and
// separate because the judge path builds a prompt naming files and wants the
// absolute form.
func resolveScriptPath(dir, script string) string {
	if filepath.IsAbs(script) {
		return script
	}
	return filepath.Join(dir, script)
}
