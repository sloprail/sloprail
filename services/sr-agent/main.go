// Command sr-agent runs an agent without the caller naming the harness that
// will run it.
//
// This is what a judging guardrail is built on. A rule asking whether code
// actually upholds an invariant needs an agent to ask, and a rule that named
// Claude Code to ask it would be a rule that only works where Claude Code is.
//
// It is its own STANDALONE binary, not a subcommand of `sloprail` — the shape
// every high-level command here is moving to: one binary per command, with a
// root `sr` that proxies to them.
//
//	sr-agent --model size-md "does this uphold the invariant?"
//	sr-agent --model claude-opus-5,size-lg --prompt "$(cat question.txt)"
//
// The prompt is positional, following the harnesses themselves: `claude
// [options] [prompt]` and `cursor-agent [options] [prompt...]` both take it
// that way, and neither has a `run` subcommand. An author who knows one of
// those already knows this.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitCode(err))
	}
}

// exitCode maps a failure to a process status.
//
// A refusal to run and a failure OF the run are both non-zero, but they are not
// the same event and a caller scripting around this needs to tell them apart: a
// hook seeing 2 knows its own command was wrong, while a hook seeing the
// harness's own code knows the agent ran and what it concluded. Collapsing both
// to 1 would make a malformed model set indistinguishable from an agent that
// ran and disagreed.
func exitCode(err error) int {
	var runErr *harnessRunError
	if errors.As(err, &runErr) {
		return runErr.code
	}
	return 2
}

// harnessRunError is the harness exiting non-zero. It carries the code so it
// can be passed through rather than flattened.
type harnessRunError struct {
	binary string
	code   int
}

func (e *harnessRunError) Error() string {
	return fmt.Sprintf("%s exited with status %d", e.binary, e.code)
}

func newRoot() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sr-agent [flags] <prompt>",
		Short: "Run an agent, whichever harness is running",
		Long: `Run an agent without naming the harness that will run it.

The prompt is positional, as every harness takes it. --prompt is accepted
instead, for when the prompt comes from a file or a pipe and a positional
would be awkward.

  sr-agent --model size-md "does this uphold the invariant?"
  sr-agent --model claude-opus-5,size-lg --prompt "$(cat question.txt)"

MODEL SETS

--model takes preferences in order, first match wins. Two kinds of entry:

  size-xs size-sm size-md size-lg size-xl size-xxl
      A size, not a model. Every harness maps every one of them, so an alias
      ALWAYS resolves — which means anything after an alias in a set can never
      be reached. Writing size-md,claude-opus-5 probably meant the other order.

  claude-opus-5, sonnet, ...
      One harness's own model. Matches only under that harness, so a set may
      name several harnesses' models and mean "the best available here".

A set matching nothing under the running harness is REFUSED, not quietly run
on a default. A verdict from a model the author did not choose is one nobody
can account for.

HARNESS

Which harness is running is read from the environment, not asked for. An
environment naming no known harness is refused rather than guessed at; pass
--harness for a hook running outside any harness at all.`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runAgent,
	}

	// A prompt beginning with a dash is read as a flag, exactly as `claude`
	// reads one — so refusing matches the convention and is not worth
	// overriding. What IS worth adding is the way out: cobra's own message
	// names the offending token and stops, leaving an author to guess. A
	// question like "--model isn't resolving, why?" is an ordinary thing to ask
	// a judge, and both escapes are non-obvious.
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return fmt.Errorf(
			"%w\n"+
				"If that was meant to be the prompt rather than a flag, pass it after -- "+
				"(sr-agent --model size-md -- \"--like this\") or with --prompt",
			err)
	})

	cmd.Flags().String("model", "",
		"Comma-separated preferences, first match wins: size aliases (size-xs…size-xxl) and/or model names")
	cmd.Flags().String("prompt", "",
		"The prompt, when a positional would be awkward (from a file or a pipe)")
	cmd.Flags().String("harness", "",
		"Run this harness instead of the one the environment names ("+strings.Join(supportedNames(), ", ")+")")
	cmd.Flags().String("claude-args", "",
		`Claude Code's own settings as a JSON object, passed through untouched (e.g. '{"permission-mode":"plan"}')`)
	cmd.Flags().String("verify", "",
		"A script that decides whether the agent's answer is acceptable; the agent is asked again if not")
	cmd.Flags().Int("verify-attempts", DefaultVerifyAttempts,
		"How many times the agent may be asked before --verify reports failure")
	cmd.Flags().Bool("dry-run", false,
		"Print the command that would run, and do not run it")

	return cmd
}

func runAgent(cmd *cobra.Command, args []string) error {
	// The registry is checked before anything is resolved, so a harness entry
	// missing a size fails here rather than at the first set that names it.
	if err := aliasesComplete(); err != nil {
		return err
	}

	modelSet, _ := cmd.Flags().GetString("model")
	promptFlag, _ := cmd.Flags().GetString("prompt")
	harnessFlag, _ := cmd.Flags().GetString("harness")
	claudeArgs, _ := cmd.Flags().GetString("claude-args")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	verifyFlag, _ := cmd.Flags().GetString("verify")
	verifyAttempts, _ := cmd.Flags().GetInt("verify-attempts")

	prompt, err := resolvePrompt(args, promptFlag)
	if err != nil {
		return err
	}

	spec, err := ResolveHarness(harnessFlag, os.Getenv)
	if err != nil {
		return err
	}

	// Checked before the model set, so a caller who got BOTH wrong hears about
	// the harness mismatch first — it is the one that explains the other.
	if cmd.Flags().Changed("claude-args") {
		if err := CheckHarnessArgs("--claude-args", ClaudeCode, spec); err != nil {
			return err
		}
	}

	prefs, err := ParseModelSet(modelSet)
	if err != nil {
		return err
	}

	resolution, err := ResolveModelSet(prefs, spec)
	if err != nil {
		return err
	}

	harnessArgs, err := ParseHarnessArgs(claudeArgs)
	if err != nil {
		return err
	}

	// Diagnostics go to stderr so that stdout carries only the agent's answer.
	// A hook capturing this command's output to feed a rule must not find
	// advice about model sets mixed into what the agent said.
	reportResolution(cmd.ErrOrStderr(), spec, resolution)

	// --verify changes the shape of the run: the agent writes to a file, the
	// caller's script judges the file, and a rejection means asking again with
	// the script's own complaint quoted back. Handled before the plain path
	// because it OWNS the prompt — it appends the output path, and on a retry
	// appends the objection too.
	if cmd.Flags().Changed("verify") {
		if verifyAttempts < 1 {
			return fmt.Errorf(
				"--verify-attempts must be at least 1, got %d: zero attempts would run no agent at all",
				verifyAttempts)
		}
		return runVerified(cmd, spec, resolution.Model, harnessArgs, prompt,
			verifyFlag, verifyAttempts, false, dryRun)
	}

	inv := BuildInvocation(spec, resolution.Model, harnessArgs, prompt)

	if dryRun {
		fmt.Fprintln(cmd.OutOrStdout(), inv.String())
		return nil
	}

	return runHarness(cmd, inv)
}

// resolvePrompt takes the prompt from wherever it was given.
//
// Both forms at once is refused rather than one silently winning: they disagree
// about what to ask, and picking either means asking a question the author did
// not write. Positional words are joined, following `cursor-agent [prompt...]`,
// so an unquoted prompt does not become a wrong-arity error.
func resolvePrompt(args []string, promptFlag string) (string, error) {
	positional := strings.TrimSpace(strings.Join(args, " "))
	fromFlag := strings.TrimSpace(promptFlag)

	switch {
	case positional != "" && fromFlag != "":
		return "", errors.New(
			"a prompt was given both positionally and with --prompt; they disagree about what to ask, so pass only one")
	case positional != "":
		return positional, nil
	case fromFlag != "":
		return fromFlag, nil
	default:
		return "", errors.New(
			"no prompt: pass it positionally (sr-agent --model size-md \"question\") or with --prompt")
	}
}

// reportResolution says which model was chosen and what the set implied.
//
// Skipped entries are named because an author whose first choice was passed
// over should learn it happened rather than wonder why a later model ran.
// Unreachable entries are named because they are almost always a mistake — an
// alias sitting in front of the models the author actually wanted.
func reportResolution(w interface{ Write([]byte) (int, error) }, spec harnessSpec, res Resolution) {
	fmt.Fprintf(w, "sr-agent: harness %s, model %s (from %q)\n",
		spec.name, res.Model, res.Matched.Raw)

	if len(res.Skipped) > 0 {
		fmt.Fprintf(w, "sr-agent: skipped %s — not offered by %s\n",
			strings.Join(rawNames(res.Skipped), ", "), spec.name)
	}

	if len(res.Unreachable) > 0 {
		fmt.Fprintf(w,
			"sr-agent: %s can never be reached — %q is a size alias, which resolves under every harness, "+
				"so the search ends there. Did you mean the other order?\n",
			strings.Join(rawNames(res.Unreachable), ", "), res.Matched.Raw)
	}
}

// runHarness execs the harness, wiring its streams straight through.
//
// The agent's answer is the whole point of this command, so stdout is passed
// through untouched rather than captured and re-emitted: a caller piping this
// gets exactly what the harness wrote, and a long answer streams rather than
// buffering. stdin is inherited so a harness reading a piped prompt still can.
func runHarness(cmd *cobra.Command, inv Invocation) error {
	// cobra populates the context during Execute, but a command that was never
	// executed has none, and exec.CommandContext PANICS on a nil one rather
	// than treating it as "no deadline". A panic here would surface as a crash
	// in whichever hook called this, so the nil is handled rather than assumed
	// away.
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}

	proc := exec.CommandContext(parent, inv.Binary, inv.Args...)
	proc.Stdin = cmd.InOrStdin()
	proc.Stdout = cmd.OutOrStdout()
	proc.Stderr = cmd.ErrOrStderr()

	err := proc.Run()
	if err == nil {
		return nil
	}

	// A missing binary is the common failure and deserves better than "file not
	// found": the harness was detected from the environment, so the caller's
	// question is why the thing that is supposedly running cannot be found.
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf(
			"%s is not on PATH, but the environment says %s is running: %w",
			inv.Binary, inv.Binary, err)
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &harnessRunError{binary: inv.Binary, code: exitErr.ExitCode()}
	}

	return fmt.Errorf("running %s: %w", inv.Binary, err)
}
