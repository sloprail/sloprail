package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
)

// score runs the fixture's scorer script and reports its verdict.
//
// The contract mirrors a guardrail script check exactly (authoring-guardrails
// skill, "the refusal contract"): exit 0 is pass, non-zero is fail, and the
// reason is read in the same preference order — {"reason":"…"} JSON on
// stdout, else plain stdout, else plain stderr. Reusing that contract rather
// than inventing a scorer-specific one means an author who has written a
// script check already knows how to write a scorer.
//
// The scorer receives, as environment variables:
//
//	SR_EVAL_TRANSCRIPT   absolute path to the agent-under-test's .jsonl
//	SR_EVAL_PROJECT_DIR  absolute path to the seeded, possibly-mutated project
//	SR_EVAL_FIXTURE_DIR  absolute path to the fixture directory itself
//	SR_EVAL_BIN_DIR      directory holding sr-session/sr-file/sr-mark/sr-agent,
//	                     so a scorer can run `sr-session query` against the
//	                     transcript without guessing where those binaries are
//
// A scorer that cannot run at all — missing, not executable, times out — is a
// FAILED eval, not a skipped one, for the same reason a check that cannot run
// must not read as a guardrail's approval: an eval nobody could actually score
// proves nothing, and reporting that as a pass would be worse than reporting
// nothing.
func score(ctx context.Context, fx Fixture, ws *workspace, transcriptPath, binDir string) (passed bool, reason string, err error) {
	cmd := exec.CommandContext(ctx, fx.ScorePath())
	cmd.Dir = fx.Dir
	cmd.Env = append(os.Environ(),
		"SR_EVAL_TRANSCRIPT="+transcriptPath,
		"SR_EVAL_PROJECT_DIR="+ws.project,
		"SR_EVAL_FIXTURE_DIR="+fx.Dir,
		"SR_EVAL_BIN_DIR="+binDir,
	)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if runErr == nil {
		return true, "", nil
	}

	if _, ok := runErr.(*exec.ExitError); !ok {
		// The script itself could not be run (not executable, missing
		// interpreter, timeout) — that is the "scorer could not run" case
		// the caller reports as exit 2, distinct from a script that ran and
		// said no.
		return false, "", runErr
	}

	return false, scorerReason(stdout.String(), stderr.String()), nil
}

// scorerReason picks the reason to report, in the same order a guardrail
// script check's refusal is read: JSON {"reason":"…"} on stdout, else plain
// stdout, else plain stderr, else a message naming that neither said anything.
func scorerReason(stdout, stderr string) string {
	out := strings.TrimSpace(stdout)
	if out != "" {
		var parsed struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal([]byte(out), &parsed) == nil && parsed.Reason != "" {
			return parsed.Reason
		}
		return out
	}
	if s := strings.TrimSpace(stderr); s != "" {
		return s
	}
	return "scorer exited non-zero with no reason on stdout or stderr"
}
