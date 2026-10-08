package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/procgroup"
)

// BuildInteractiveInvocation is BuildInvocation for the harness's interactive (TUI) mode:
// no one-shot words (`-p`, `exec`) and no prompt, which the caller types on the terminal.
// The flags are the same, so the run carries the settings (--trust, --force, the model)
// the one-shot agent run does.
func BuildInteractiveInvocation(spec harnessSpec, model string, harnessArgs []string, getenv func(string) string) Invocation {
	args := []string{spec.modelFlagOrDefault(), model}
	args = append(args, spec.baseArgs...)
	args = append(args, harnessArgs...)
	return Invocation{Binary: resolveBinary(spec, getenv), Args: args}
}

// runInteractiveHarness execs the harness in its interactive mode on the terminal this
// process runs on: the child gets the real stdin, stdout and stderr, because a TUI needs a
// terminal on all of them (a pipe, as runHarness hands a one-shot run, would make it fall
// back or refuse). The caller that drives it owns that terminal, and with it the output;
// there is no answer to tail, so a failure carries only the exit status.
func runInteractiveHarness(cmd *cobra.Command, inv Invocation) error {
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	proc := exec.CommandContext(parent, inv.Binary, inv.Args...)
	proc.Stdin, proc.Stdout, proc.Stderr = os.Stdin, os.Stdout, os.Stderr
	proc.Env = append(sanitizeChildEnv(os.Environ()), inv.Env...)

	// Not in a group of its own: it must stay in the terminal's foreground group to read it.
	defer procgroup.ExitOnSignal(nil)()
	err := procgroup.Run(proc, false)
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s is not on PATH: %w", inv.Binary, err)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &harnessRunError{binary: inv.Binary, code: exitErr.ExitCode()}
	}
	return fmt.Errorf("running %s: %w", inv.Binary, err)
}
