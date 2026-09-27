package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	allowedTools []string,
	addDirs []dirGrant,
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
	//
	// The answer file's folder is one more WRITABLE dir, granted through the
	// same path as a caller's `--add-dir` — MERGED with the caller's dirs and
	// allowed-tools into one `--add-dir`, one `--allowed-tools`, never competing
	// variadic groups. See claudeCodeSpec.grant for what each grants and what was
	// measured.
	dirs := append(append([]dirGrant{}, addDirs...), dirGrant{Path: filepath.Dir(outputPath), Mode: dirWritable})
	grantArgs, err := harnessGrant(spec, accessGrant{Dirs: dirs, Tools: allowedTools})
	if err != nil {
		return err
	}
	harnessArgs = append(harnessArgs, grantArgs...)

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
		fmt.Fprintln(cmd.OutOrStdout(), BuildInvocation(spec, model, harnessArgs, ask, os.Getenv).String())
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s   # verdict: exit 0 accepts, non-zero re-asks (up to %d attempts)\n",
			resolved, outputPath, attempts)
		return nil
	}

	var lastRejection error
	for attempt := 1; attempt <= attempts; attempt++ {
		// Removed between attempts, so a second run that writes nothing at all
		// cannot pass on the first attempt's leftovers. Without this the retry
		// would silently re-judge stale bytes and report a pass for an agent that
		// did nothing. Removed rather than truncated: the file must not EXIST when
		// the agent starts (see makeOutputFile).
		if err := os.Remove(outputPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("%w: could not clear %s: %s", ErrVerifierBroken, outputPath, err)
		}

		inv := BuildInvocation(spec, model, harnessArgs, ask, os.Getenv)
		said, err := runAgentQuietly(parent, cmd, inv)
		if err != nil {
			// The harness itself failed — not a verdict on the answer. Returned
			// rather than retried: re-running a harness that could not start
			// spends money to fail identically.
			return err
		}
		if err := answerFromReply(outputPath, said); err != nil {
			return fmt.Errorf("%w: could not write %s: %s", ErrVerifierBroken, outputPath, err)
		}

		err = RunVerifier(parent, resolved, outputPath, attempt, attempts, cmd.ErrOrStderr())
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
		var rej *verifyRejection
		if errors.As(err, &rej) && rej.final() {
			// A well-formed "no" (FinalRejectionExit): the answer is the answer.
			return err
		}
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

// makeOutputFile picks the path the agent writes its answer to, in a directory
// sr-agent owns, and returns a cleanup.
//
// sr-agent owns the path rather than taking one from the caller. A
// caller-chosen path could sit inside the tree the agent is working in, where
// the agent's own edits would collide with it — and the file's value is that it
// does not exist until the agent writes it, is removed between attempts, and is
// removed at the end, none of which sr-agent can promise about a path someone
// else picked.
//
// The DIRECTORY is created; the file is NOT. It used to be created empty, so
// that "the agent wrote nothing" reached the verifier as an empty file. But
// Claude Code's Write tool refuses to overwrite an existing file the agent has
// not Read first, so every judge spent one tool call on that refusal before
// writing its verdict — measured on claude 2.1.282 (haiku, 2026-09-27): 5 of 5
// real judge runs with the pre-created file hit "File has not been read yet.
// Read it first before writing to it." and then Read and re-Wrote it (tool
// calls: Read, Write, Read, Write); 0 of 5 hit it with no file there, the first
// Write creating it under the Edit(//<dir>/**) grant (tool calls: Read, Write).
//
// Nothing is lost by it. "Never written" is now a MISSING file, which
// RunVerifier reports itself as a failed attempt ("the agent wrote no output
// to …") and the retry quotes back to the agent; "written empty" is an empty
// file, which the caller's verifier judges as before. The two used to look the
// same.
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
		return path, cleanup, nil
	}

	// A directory supplied for tests. Not removed, because it is not ours — but
	// an answer left there by an earlier run is, and it must not be judged as
	// this run's.
	path = filepath.Join(dir, "answer")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", func() {}, fmt.Errorf("%w: could not clear %s: %s", ErrVerifierBroken, path, err)
	}
	return path, func() { _ = os.Remove(path) }, nil
}

// runAgentQuietly runs the harness with its stdout suppressed.
//
// The agent's prose is not the answer here — the FILE is. Letting the harness
// write to stdout would mix a model's chatter into what a hook reads, and a
// hook that captures this command's output to feed a rule must find only the
// answer. stderr still passes through, because that is where a harness reports
// its own trouble and a caller needs to see it.
//
// stdin is EMPTY, not inherited. Under --verify sr-agent owns the whole prompt,
// so nothing on stdin is meant for the agent — and `claude -p` reads its stdin
// and appends it to the prompt. Inherited, a stdin that is open but silent (a
// terminal, a script's pipe nobody closes) held every run for claude's own
// timeout: measured on claude 2.1.282 (haiku, 2026-09-27), a one-Read judge
// call with an open, silent stdin printed "Warning: no stdin data received in
// 3s, proceeding without it" and took 11.85-15.90s (3 runs) against
// 9.08-10.63s (2 runs) with stdin closed — the 3s wait, per call. With this
// empty stdin (and no pre-created answer file, see makeOutputFile) the same
// call took 6.12-6.90s with the caller's stdin open and 5.93-5.96s closed, and
// never printed the warning. A stdin carrying data is worse: a
// script check that runs sr-agent would hand the agent its own check payload
// as part of the question. The plain (non --verify) path still inherits stdin,
// because there the caller may be piping the prompt on purpose.
func runAgentQuietly(ctx context.Context, cmd *cobra.Command, inv Invocation) (string, error) {
	quiet := *cmd
	reply := &cappedBuffer{max: maxReply}
	quiet.SetOut(reply)
	quiet.SetIn(strings.NewReader(""))
	quiet.SetContext(ctx)
	err := runHarness(&quiet, inv)
	return reply.String(), err
}

// maxReply bounds how much of the agent's printed reply is kept — enough for a
// verdict object, never a whole transcript.
const maxReply = 64 << 10

// cappedBuffer keeps the first max bytes written to it and drops the rest,
// reporting every write as complete so the harness is never cut short.
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string { return c.buf.String() }

// answerFromReply fills an answer file the agent left EMPTY from its printed
// reply, when that reply is exactly one JSON object. A model asked for a verdict
// sometimes prints it instead of writing it — measured on real judges — and a
// retry then pays for a second run to re-state what it already said. The
// verifier still judges the bytes; a reply that is not a single object, or a
// file the agent did write, is left untouched.
func answerFromReply(outputPath, reply string) error {
	// Filled when the agent left no answer — the file missing (never written) or
	// empty — and never over an answer it did write.
	if info, err := os.Stat(outputPath); err == nil && info.Size() > 0 {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return nil
	}
	reply = strings.TrimSpace(reply)
	if !strings.HasPrefix(reply, "{") || !json.Valid([]byte(reply)) {
		return nil
	}
	return os.WriteFile(outputPath, []byte(reply+"\n"), 0o600)
}

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
