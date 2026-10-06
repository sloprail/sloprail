package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/procgroup"
)

// ErrVerifyFailed is returned when the verifier rejected the agent's output and
// the attempts ran out.
var ErrVerifyFailed = errors.New("verification failed")

// ErrVerifierBroken is returned when the verifier itself could not run.
var ErrVerifierBroken = errors.New("verifier could not run")

// OutputPathEnv names the file the launched agent is told to write.
//
// The agent learns the path from the PROMPT, not from this variable — a model
// does not read the environment. The variable exists for the VERIFIER, which
// does, and which would otherwise have to be told the path by a caller that
// does not know it either (sr-agent makes it). The verifier also gets it as
// argv[1]; both, because a shell script reaches for "$1" and a Python one for
// os.environ, and neither should have to learn the other's convention.
const OutputPathEnv = "SLOPRAIL_AGENT_OUTPUT"

// AttemptEnv tells the verifier which attempt it is judging, 1-based.
//
// A verifier that wants to be lenient on the last attempt, or to log only the
// final failure, cannot do so without knowing. Exposed rather than left
// implicit because the retry count is sr-agent's decision, not the caller's,
// and a verifier guessing at it would be coupled to a default it cannot see.
const AttemptEnv = "SLOPRAIL_ATTEMPT"

// AttemptsEnv is how many attempts there are in total.
const AttemptsEnv = "SLOPRAIL_ATTEMPTS"

// DefaultVerifyAttempts is how many times the agent is asked before the run is
// reported failed.
//
// TWO, not one and not five.
//
// One would make --verify a pure gate: it would catch the bad output and have
// nothing to do about it, which the caller could already have done itself by
// running the script after sr-agent. The whole point of passing the script IN
// is that sr-agent can act on the verdict.
//
// More than two buys very little and costs real money. The second attempt is
// the one that fixes a forgotten field or a wrong shape, because it is the one
// where the agent is TOLD what was wrong. If a model that has been shown the
// verifier's own complaint still cannot satisfy it, the third attempt is
// usually the same model making the same mistake at the same price — and this
// runs inside a hook, where the caller is waiting. A judge is not a build.
//
// The caller can override with --verify-attempts. It is a default, not a law;
// what it is not is unbounded, because an unbounded retry inside a Stop hook is
// how a guardrail becomes a bill. That last clause was a claim about the
// DEFAULT for as long as it stood alone, and it was read as one about the flag —
// see MaxVerifyAttempts, which is the bound it was describing.
const DefaultVerifyAttempts = 2

// MaxVerifyAttempts is the ceiling the paragraph above was asserting and the
// flag did not have.
//
// "What it is not is unbounded" described the DEFAULT and was read as describing
// the flag. It was not: the lower bound was checked and nothing checked the
// other end, so `--verify-attempts 100000` was taken verbatim and became 100000
// real `claude` launches — the loop in verify_run.go is serial, has no timeout,
// and on the rejecting path never exits early, so it runs every one of them. The
// comment naming that exact failure ("how a guardrail becomes a bill") sat six
// lines above the flag that permitted it.
//
// Twenty is chosen to be far above any real use and far below a bill. The
// paragraph above argues a third attempt is usually the same model making the
// same mistake at the same price, so the honest ceiling is nearer three — but
// this is a ceiling on ABSURDITY, not a second opinion about what a good number
// is. It refuses the typo and the mis-expanded shell variable, which are the
// ways a large number actually arrives, and leaves every deliberate choice
// alone.
//
// A ceiling rather than a silent clamp. Clamping would run 20 attempts for a
// caller who asked for 100000 and report success, which is the engine deciding
// on its own account and hiding that it did; the caller asked for something this
// will not do, and is told so.
const MaxVerifyAttempts = 20

// checkVerifyAttempts holds the flag to both ends of its range.
//
// One function rather than two inline comparisons, because the two bounds are
// one question — is this a number of agent launches worth making — and the
// caller that asked only the lower half is exactly the defect this replaces.
func checkVerifyAttempts(n int) error {
	if n < 1 {
		return fmt.Errorf(
			"--verify-attempts must be at least 1, got %d: zero attempts would run no agent at all", n)
	}
	if n > MaxVerifyAttempts {
		return fmt.Errorf(
			"--verify-attempts must be at most %d, got %d: each attempt is a real agent launch, and %d of them is a bill rather than a retry",
			MaxVerifyAttempts, n, n)
	}
	return nil
}

// outputDirEnv overrides where the agent's output file is created.
//
// For tests only, and deliberately not a flag. A caller who could choose the
// path could choose one inside the tree the agent is working in, and the file's
// whole value is that sr-agent owns it: it does not exist until the agent
// writes it, is removed between attempts, and is removed afterwards. A
// caller-supplied path breaks all three.
const outputDirEnv = "SLOPRAIL_AGENT_OUTPUT_DIR"

// ResolveVerifier turns a --verify value into a runnable command.
//
// An executable file is run directly. A file that exists but is not executable
// is REFUSED rather than run through a shell: guessing an interpreter for a
// caller's script means guessing wrong for a Python one that opens with a
// shebang the caller expected to be honoured, and a silent `sh` fallback would
// turn their SyntaxError into a shell error about an unexpected token. The
// missing chmod is a one-line fix and the message says so.
func ResolveVerifier(script string) (string, error) {
	trimmed := strings.TrimSpace(script)
	if trimmed == "" {
		return "", fmt.Errorf("%w: --verify was given an empty path", ErrVerifierBroken)
	}

	// A bare name is resolved on PATH, as a shell would; anything with a
	// separator is a path and is resolved relative to the caller's cwd.
	if !strings.ContainsRune(trimmed, filepath.Separator) {
		found, err := exec.LookPath(trimmed)
		if err != nil {
			return "", fmt.Errorf(
				"%w: %q is not on PATH; pass a path like ./verify.sh if it is a file here",
				ErrVerifierBroken, trimmed)
		}
		return found, nil
	}

	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %s", ErrVerifierBroken, trimmed, err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %s", ErrVerifierBroken, trimmed, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%w: %s is a directory", ErrVerifierBroken, trimmed)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf(
			"%w: %s is not executable. Run chmod +x %s — sr-agent will not guess an "+
				"interpreter for it, because guessing wrong reports your script's error as a shell error",
			ErrVerifierBroken, trimmed, trimmed)
	}
	return abs, nil
}

// verifyPromptSuffix tells the agent where to put its answer.
//
// Appended to the caller's prompt rather than replacing it, and phrased as an
// instruction about the FILE rather than about formatting, because the file is
// the contract: the verifier reads the file, not the transcript. An agent that
// answers in prose and writes nothing fails verification — the file is missing,
// and RunVerifier says so — which is the correct outcome and a legible one.
func verifyPromptSuffix(outputPath string) string {
	return fmt.Sprintf(
		"\n\nWrite your answer to the file %s. That file is what will be read and checked; "+
			"anything you say outside it is ignored. Write the file before you finish.",
		outputPath)
}

// retryPromptSuffix tells the agent why it is being asked again.
//
// The verifier's own stderr is quoted back verbatim. This is the entire reason
// a second attempt is worth paying for: an agent told only "that was wrong"
// will usually produce a different wrong answer, while one shown the checker's
// actual complaint is being asked a much easier question. It also means the
// quality of the retry is the caller's to control — a verifier that explains
// itself gets better second attempts, which is the right incentive.
func retryPromptSuffix(outputPath, complaint string) string {
	complaint = strings.TrimSpace(complaint)
	if complaint == "" {
		complaint = "(the check failed but printed no explanation)"
	}
	return fmt.Sprintf(
		"\n\nA previous attempt wrote %s and it was REJECTED by the check that must pass. "+
			"The check said:\n\n%s\n\nFix exactly that and write the corrected answer to %s again.",
		outputPath, complaint, outputPath)
}

// RunVerifier runs the caller's script against the agent's output file.
//
// WHAT THE SCRIPT RECEIVES:
//
//   - argv[1]: the path to the file the agent wrote.
//   - SLOPRAIL_AGENT_OUTPUT: the same path.
//   - SLOPRAIL_ATTEMPT / SLOPRAIL_ATTEMPTS: which try this is, and of how many.
//   - stdin: the file's contents, so a one-line `jq -e .pass` works with no
//     argument handling at all.
//
// The FILE and not the transcript. A transcript is the harness's own format —
// Claude Code writes JSONL at a path only it defines — and a verifier that
// parsed one would be a verifier that only works under Claude Code, which is
// the coupling this whole binary exists to avoid. The file is a contract
// sr-agent owns and every harness can satisfy.
//
// Exit status is the verdict: zero passes, non-zero fails. That is the
// convention every check in a shell already uses, so `jq -e`, `test`, `grep -q`
// and a Python `sys.exit(1)` all work as verifiers with nothing adapted.
func RunVerifier(ctx context.Context, verifier, outputPath string, attempt, attempts int, stderr io.Writer) error {
	content, err := os.ReadFile(outputPath)
	if err != nil {
		// The agent never wrote the file. That is a verification FAILURE, not a
		// broken verifier: the agent was told to write it and did not, which is
		// exactly the thing --verify is for catching. Reported as a failure so
		// it consumes an attempt and the agent gets told about it.
		return fmt.Errorf("%w: the agent wrote no output to %s", ErrVerifyFailed, outputPath)
	}

	proc := exec.CommandContext(ctx, verifier, outputPath)
	proc.Stdin = strings.NewReader(string(content))
	// The verifier runs in its own group: a timeout kills the group, not just the script.
	proc.Cancel = func() error { return procgroup.KillGroup(proc.Process.Pid) }
	proc.Env = append(os.Environ(),
		OutputPathEnv+"="+outputPath,
		fmt.Sprintf("%s=%d", AttemptEnv, attempt),
		fmt.Sprintf("%s=%d", AttemptsEnv, attempts),
	)

	// The verifier's own output is captured rather than streamed, because it is
	// quoted back to the agent on a retry. It is also echoed to stderr so a
	// human watching sees why a run is being repeated.
	var complaint strings.Builder
	proc.Stdout = &complaint
	proc.Stderr = &complaint

	runErr := procgroup.Run(proc, true)
	if complaint.Len() > 0 {
		fmt.Fprintf(stderr, "sr-agent: verifier (attempt %d/%d): %s\n",
			attempt, attempts, strings.TrimSpace(complaint.String()))
	}
	if runErr == nil {
		return nil
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return &verifyRejection{complaint: complaint.String(), code: exitErr.ExitCode()}
	}

	// The script could not be executed at all — not a verdict, a broken tool.
	//
	// FAIL-CLOSED, matching the engine's default. A verifier that cannot run has
	// checked nothing, and treating "unchecked" as "passed" is how a guardrail
	// stops guarding without anybody noticing: the caller sees a zero exit and
	// concludes the output was validated. Failing here is loud and wrong-way-
	// safe — the caller finds out their script is broken the first time it
	// matters, rather than discovering months later that nothing was checked.
	return fmt.Errorf("%w: %s: %s", ErrVerifierBroken, verifier, runErr)
}

// verifyRejection is the verifier exiting non-zero: a real verdict, carrying
// what it said so the next attempt can be told.
type verifyRejection struct {
	complaint string
	code      int
}

// FinalRejectionExit is the verifier exit code for "the answer is well formed
// and it is a no": sr-agent reports the rejection at once instead of asking
// again. Every other non-zero exit means the answer itself was unusable and is
// worth re-asking. A judge needs the difference: its verifier used to exit 1
// for a clean "fail" verdict too, so every legitimate refusal was sent back to
// the judge as REJECTED with "fix exactly that" — two model runs per refusal,
// and a prompt pushing the judge to reverse a correct verdict.
const FinalRejectionExit = 3

// final reports whether the verifier said this rejection is not worth a retry.
func (e *verifyRejection) final() bool { return e.code == FinalRejectionExit }

func (e *verifyRejection) Error() string {
	detail := strings.TrimSpace(e.complaint)
	if detail == "" {
		return fmt.Sprintf("%s: the check exited %d", ErrVerifyFailed, e.code)
	}
	return fmt.Sprintf("%s: %s", ErrVerifyFailed, detail)
}

func (e *verifyRejection) Is(target error) bool { return target == ErrVerifyFailed }
