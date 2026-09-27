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
	"github.com/spf13/pflag"

	"github.com/sloprail/sloprail/internal/version"
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
		Version:       version.Version,
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
		// An `--add-dir:<mode>` with a mode that does not exist is a usage error
		// about the MODE, not a prompt that happens to start with a dash.
		if modeErr := unknownAddDirMode(err); modeErr != nil {
			return modeErr
		}
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
	cmd.Flags().String("allowed-tools", "",
		"Tools the agent may use, comma- or space-separated (maps to the harness's own allowed-tools; e.g. \"Read WebFetch\")")
	// One repeatable flag per --add-dir mode: `--add-dir`, `--add-dir:readonly`.
	for _, m := range addDirModes {
		cmd.Flags().StringArray(addDirFlagName(m.suffix), nil, m.usage)
	}
	cmd.Flags().String("verify", "",
		"A script that decides whether the agent's answer is acceptable: exit 0 accepts, exit 3 rejects it as final, any other non-zero asks the agent again")
	cmd.Flags().Int("verify-attempts", DefaultVerifyAttempts,
		fmt.Sprintf("How many times the agent may be asked before --verify reports failure (1-%d)", MaxVerifyAttempts))
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
	allowedToolsFlag, _ := cmd.Flags().GetString("allowed-tools")
	// Read through the flag's own slice, not GetStringArray: that one round-trips
	// the values through CSV and loses an empty `--add-dir:readonly=`, which
	// must be refused, not dropped.
	addDirFlags := map[dirMode][]string{}
	for _, m := range addDirModes {
		if f := cmd.Flags().Lookup(addDirFlagName(m.suffix)); f != nil {
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				addDirFlags[m.mode] = sv.GetSlice()
			}
		}
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	verifyFlag, _ := cmd.Flags().GetString("verify")
	verifyAttempts, _ := cmd.Flags().GetInt("verify-attempts")

	// Judged HERE rather than inside the --verify branch below, so the flag has
	// one meaning whatever else was passed. The check used to sit under
	// `Changed("verify")`, which made `--verify-attempts 0` an error with
	// --verify and silently fine without it — one flag, two answers, and the
	// permissive one reached by leaving a flag off. Where the value is unused
	// the refusal costs a caller nothing they wanted; being told a number is
	// nonsense is better than having it quietly ignored.
	if cmd.Flags().Changed("verify-attempts") {
		if err := checkVerifyAttempts(verifyAttempts); err != nil {
			return err
		}
	}

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

	// The tools the agent may use, parsed from the comma/space-separated flag into
	// the list the harness merges with the write grant it needs. Empty when the
	// flag was not given, which grants only the answer-file write --verify needs.
	allowedTools, err := ParseAllowedTools(allowedToolsFlag)
	if err != nil {
		return err
	}

	// The directories the agent is given, each in its mode, made absolute and
	// checked to exist BEFORE anything runs: a harness permission rule is matched
	// on an absolute path, and a typo'd directory is free to report now and
	// expensive to discover as a blind judge after a model has been paid for.
	addDirs, err := resolveAddDirs(addDirFlags)
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
		return runVerified(cmd, spec, resolution.Model, harnessArgs, allowedTools, addDirs, prompt,
			verifyFlag, verifyAttempts, false, dryRun)
	}

	// Outside --verify there is no answer file: the grant carries only the
	// caller's own dirs and tools.
	grantArgs, err := harnessGrant(spec, accessGrant{Dirs: addDirs, Tools: allowedTools})
	if err != nil {
		return err
	}
	harnessArgs = append(harnessArgs, grantArgs...)

	inv := BuildInvocation(spec, resolution.Model, harnessArgs, prompt, os.Getenv)

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

// childEnvBlocklist is the set of environment variables that name the CURRENT,
// still-live Claude Code session and must never reach a nested harness
// invocation. sr-agent's own most important caller is a guardrail firing from
// WITHIN an already-running, interactive Claude Code session, which shells out
// to spawn a nested, one-shot `claude -p` judge call — a call that is meant to
// be its own independent session, not a second voice inside the parent's.
//
// Go's exec.Cmd inherits the FULL process environment by default whenever
// Env is left nil, and main.go used to leave it nil: proc.Env was never set,
// so every one of these rode along into the child unchanged. That is a strong
// candidate for the child colliding with the parent's own live session/IPC
// state — CLAUDE_CODE_SESSION_ID and CLAUDE_CODE_HOST_SESSION_ID identify the
// parent's session, and CLAUDE_CODE_MESSAGING_SOCKET / _TOKEN are the parent's
// own live IPC channel. A nested `claude -p` that inherited these would be
// telling the world it IS the parent session, or trying to speak on a socket
// the parent is still using — either one a plausible cause of the nested
// process's opaque, undetailed crash.
//
// CLAUDECODE and CLAUDE_CODE_ENTRYPOINT are deliberately NOT on this list.
// Those two are what tell a harness it is running non-interactively/headlessly
// under Claude Code (the same pair claudeCodeSpec.detect reads), which the
// nested `claude -p` still needs to behave correctly as a one-shot call rather
// than trying to start an interactive session with no terminal to attach to.
// Stripping identity/IPC state is about the child not impersonating or
// colliding with the STILL-LIVE parent session; it is not about hiding that a
// harness is present at all.
var childEnvBlocklist = map[string]bool{
	"CLAUDE_CODE_SESSION_ID":       true,
	"CLAUDE_CODE_HOST_SESSION_ID":  true,
	"CLAUDE_CODE_MESSAGING_SOCKET": true,
	"CLAUDE_CODE_MESSAGING_TOKEN":  true,
}

// sanitizeChildEnv strips the parent Claude Code session's identity and IPC
// variables out of an environment before it is handed to a nested harness
// process, so a one-shot judge call started from within a live session does
// not inherit that session's own live state. See childEnvBlocklist for which
// variables and why.
//
// Takes and returns the os.Environ() "KEY=VALUE" slice form directly — the
// same shape exec.Cmd.Env expects — rather than a map, so the caller can pass
// os.Environ()'s result straight through with no reshaping on either side and
// this function has nothing to do but filter.
//
// Everything else passes through untouched: PATH, ANTHROPIC_API_KEY (or
// whatever auth the nested claude needs to run at all), CLAUDECODE and
// CLAUDE_CODE_ENTRYPOINT, and anything this binary has no opinion about. This
// is a blocklist rather than an allowlist on purpose — an allowlist would have
// to anticipate every variable a future harness or a caller's own environment
// might need, and getting that wrong silently breaks auth or configuration
// that used to work. A blocklist only has to name the specific vars that are
// actively wrong to inherit, which is a much smaller and more stable claim.
func sanitizeChildEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		key, _, found := strings.Cut(kv, "=")
		if found && childEnvBlocklist[key] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// runHarness execs the harness, wiring its streams straight through.
//
// The agent's answer is the whole point of this command, so stdout is passed
// through untouched rather than captured and re-emitted: a caller piping this
// gets exactly what the harness wrote, and a long answer streams rather than
// buffering. stdin is inherited so a harness reading a piped prompt still can.
//
// proc.Env is set explicitly (via sanitizeChildEnv) rather than left nil.
// exec.Cmd's documented default for a nil Env is the CURRENT process's full
// environment — fine for a standalone run, but this command's most important
// caller is itself running nested inside a live Claude Code session, and that
// session's own identity/IPC variables must not ride along into a child that
// is supposed to be an independent one-shot call. See sanitizeChildEnv.
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
	proc.Env = sanitizeChildEnv(os.Environ())

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
