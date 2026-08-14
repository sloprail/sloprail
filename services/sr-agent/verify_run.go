package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// runVerified runs the agent, has the caller's script judge what it wrote, and
// asks again when the script says no.
//
// This is the shape of the whole feature, and the ordering is the argument:
//
//  1. sr-agent makes the output file and tells the AGENT about it, in the
//     prompt. The agent cannot read an environment variable; the script cannot
//     read a prompt. So the path travels by two routes to two different
//     readers, and neither has to learn the other's channel.
//  2. The agent runs, taking as many internal turns as it needs. Nothing here
//     bounds them — a judge that must read six files to reach a verdict is
//     doing its job, and a turn cap would cut it off mid-answer and report the
//     truncation as a failed check.
//  3. The script judges the FILE. Exit status is the verdict.
//  4. A rejection quotes the script's own complaint into the next prompt.
//
// Step 4 is why passing the script IN is worth more than running it after.
// A caller could always have run their own check on sr-agent's output — what
// they could not do is put the checker's complaint in front of the agent and
// ask again. An agent told "that was wrong" produces a different wrong answer;
// one shown the actual objection is answering a much easier question.
func runVerified(
	cmd *cobra.Command,
	spec harnessSpec,
	model string,
	harnessArgs []string,
	prompt string,
	verifier string,
	attempts int,
	structured bool,
	dryRun bool,
) error {
	resolved, err := ResolveVerifier(verifier)
	if err != nil {
		// Resolved BEFORE the agent runs. A typo in the script path is free to
		// discover now and expensive to discover after a model has been paid
		// for and the run is already spent.
		return err
	}

	outputPath, cleanup, err := makeOutputFile()
	if err != nil {
		return err
	}
	defer cleanup()

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}

	// The output file sits OUTSIDE the working tree on purpose — sr-agent owns
	// it — and a harness that sandboxes writes will refuse to create it.
	// Measured, not theorised: without this the agent reads the input, works
	// out the right answer, and then says it needs permission, leaving the
	// verifier to judge an empty file. A correct judgement is reported as a
	// failed one.
	if spec.grantWrite != nil {
		harnessArgs = append(harnessArgs, spec.grantWrite(filepath.Dir(outputPath))...)
	}

	// The path is appended to the caller's prompt rather than replacing it: the
	// caller's question is still the question, and this only says where the
	// answer goes.
	ask := prompt + verifyPromptSuffix(outputPath)

	if dryRun {
		// The FIRST attempt's command, because there is no second one to show:
		// what the retry looks like depends on what the script says, which
		// depends on running it. Printing one honest command beats inventing a
		// hypothetical sequence.
		// BOTH halves. Showing only the agent command would hide the mechanism
		// being configured — the whole point of --verify is the script, and a
		// caller checking their wiring needs to see which script was resolved
		// and where its verdict comes from.
		fmt.Fprintln(cmd.OutOrStdout(), BuildInvocation(spec, model, harnessArgs, ask).String())
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s   # verdict: exit 0 accepts, non-zero re-asks (up to %d attempts)\n",
			resolved, outputPath, attempts)
		return nil
	}

	var lastRejection error
	for attempt := 1; attempt <= attempts; attempt++ {
		// Truncated between attempts, so a second run that writes nothing at
		// all cannot pass on the first attempt's leftovers. Without this the
		// retry would silently re-judge stale bytes and report a pass for an
		// agent that did nothing.
		if err := os.Truncate(outputPath, 0); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("%w: could not clear %s: %s", ErrVerifierBroken, outputPath, err)
		}

		inv := BuildInvocation(spec, model, harnessArgs, ask)
		if err := runAgentQuietly(parent, cmd, inv); err != nil {
			// The harness itself failed — not a verdict on the answer. Returned
			// rather than retried: re-running a harness that could not start
			// spends money to fail identically.
			return err
		}

		err := RunVerifier(parent, resolved, outputPath, attempt, attempts, cmd.ErrOrStderr())
		if err == nil {
			// Which attempt it took, on stderr. A caller watching a hook needs
			// to know the difference between an answer that was right first
			// time and one that needed the retry — the second is still a pass,
			// but it is the signal that a prompt or a rubric is marginal.
			fmt.Fprintf(cmd.ErrOrStderr(), "sr-agent: verified on attempt %d/%d\n", attempt, attempts)
			return emitVerified(cmd, outputPath, structured)
		}
		if errors.Is(err, ErrVerifierBroken) {
			// The script could not run. Not a verdict, so not an attempt worth
			// spending — and fail-closed, per the engine's default. See
			// RunVerifier for the argument.
			return err
		}

		lastRejection = err
		if attempt < attempts {
			ask = prompt + verifyPromptSuffix(outputPath) +
				retryPromptSuffix(outputPath, err.Error())
		}
	}

	// Exhausted. The count is in the message because the bare complaint reads
	// like a single failed check, and the caller's next question is always
	// whether it was retried at all.
	// %w, not %s: wrapping keeps ErrVerifyFailed reachable by errors.Is, which
	// is how a caller tells a rejected answer from a broken verifier. Formatting
	// it with %s flattens the chain and makes the two indistinguishable.
	return fmt.Errorf("%w (after %d attempts)", lastRejection, attempts)
}

// makeOutputFile creates the file the agent writes and returns a cleanup.
//
// sr-agent owns the path rather than taking one from the caller. A
// caller-chosen path could sit inside the tree the agent is working in, where
// the agent's own edits would collide with it — and the file's value is that it
// is created empty, truncated between attempts, and removed at the end, none of
// which sr-agent can promise about a path someone else picked.
//
// A plain, unique name with a .json-free suffix: the content is whatever the
// caller's script expects, so naming it .json would be a claim this code has no
// business making.
func makeOutputFile() (path string, cleanup func(), err error) {
	dir := os.Getenv(outputDirEnv)
	if dir == "" {
		dir, err = os.MkdirTemp("", "sr-agent-output")
		if err != nil {
			return "", func() {}, fmt.Errorf("%w: %s", ErrVerifierBroken, err)
		}
		path = filepath.Join(dir, "answer")
		cleanup = func() { _ = os.RemoveAll(dir) }
	} else {
		// A directory supplied for tests. Not removed, because it is not ours.
		path = filepath.Join(dir, "answer")
		cleanup = func() { _ = os.Remove(path) }
	}

	// Created empty so the verifier's "the agent wrote nothing" case is an
	// empty file rather than a missing one. The distinction matters: a missing
	// file could mean the agent wrote elsewhere, and an empty one can only mean
	// it did not write.
	f, err := os.Create(path)
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("%w: %s", ErrVerifierBroken, err)
	}
	_ = f.Close()
	return path, cleanup, nil
}

// runAgentQuietly runs the harness with its stdout suppressed.
//
// The agent's prose is not the answer here — the FILE is. Letting the harness
// write to stdout would mix a model's chatter into what a hook reads, and a
// hook that captures this command's output to feed a rule must find only the
// answer. stderr still passes through, because that is where a harness reports
// its own trouble and a caller needs to see it.
func runAgentQuietly(ctx context.Context, cmd *cobra.Command, inv Invocation) error {
	quiet := *cmd
	quiet.SetOut(io_Discard{})
	quiet.SetContext(ctx)
	return runHarness(&quiet, inv)
}

// io_Discard is io.Discard as a Writer that cobra will accept.
type io_Discard struct{}

func (io_Discard) Write(p []byte) (int, error) { return len(p), nil }

// emitVerified writes the accepted answer to stdout.
//
// The FILE's contents, not the agent's prose — the file is what passed the
// check, so it is the only thing that has been established. A caller piping
// sr-agent into `jq` gets what their own script just approved.
func emitVerified(cmd *cobra.Command, outputPath string, structured bool) error {
	content, err := os.ReadFile(outputPath)
	if err != nil {
		return fmt.Errorf("%w: the check passed but %s could not be read: %s",
			ErrVerifierBroken, outputPath, err)
	}
	_, err = cmd.OutOrStdout().Write(content)
	return err
}
