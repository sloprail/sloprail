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
// The PASS/FAIL contract mirrors a guardrail script check exactly
// (authoring-guardrails skill, "the refusal contract"): exit 0 is pass,
// non-zero is fail, and the reason is read in the same preference order —
// {"reason":"…"} JSON on stdout, else plain stdout, else plain stderr.
// Reusing that contract rather than inventing a scorer-specific one means an
// author who has written a script check already knows how to write a
// scorer.
//
// That contract alone is SILENT on a pass, by design — an ordinary check
// exits 0 and says nothing, "silence is consent". That is right for a
// guardrail deciding whether to block an action, but wrong for an eval RUN
// meant to be archived and looked back on later: a passing run's archive
// would carry a verdict with no account of what was actually found, while a
// failing run's carries a full explanation. So a scorer may ALSO write a
// structured verdict — evidence, not just a bit — to SR_EVAL_VERDICT_OUT,
// on every run, pass or fail. This is additive: a scorer that never touches
// the file (like a bare `exit 0`/`exit 1` script) still works exactly as
// before, just with an empty verdict archived alongside it.
//
// The shape ({subject, status, rows:[{check_id, status, reasoning}]}) is
// copied from a10n-eval's own verdict.json, not invented fresh — the same
// per-check pass/fail-plus-reasoning record its scorers have written for a
// while, so a fixture author (or a reader) who has seen one already
// recognizes the other.
//
// The scorer receives, as environment variables:
//
//	SR_EVAL_TRANSCRIPT    absolute path to the agent-under-test's .jsonl
//	SR_EVAL_PROJECT_DIR   absolute path to the seeded, possibly-mutated project
//	SR_EVAL_FIXTURE_DIR   absolute path to the fixture directory itself
//	SR_EVAL_BIN_DIR       directory holding sr-session/sr-file/sr-mark/sr-agent,
//	                      so a scorer can run `sr-session query` against the
//	                      transcript without guessing where those binaries are
//	SR_EVAL_AGENT_HOME    the isolated HOME the agent ran in
//	SR_EVAL_VERDICT_OUT   a path the scorer may write a verdict JSON to — see
//	                      verdict.go for the shape
//
// A scorer that cannot run at all — missing, not executable, times out — is a
// FAILED eval, not a skipped one, for the same reason a check that cannot run
// must not read as a guardrail's approval: an eval nobody could actually score
// proves nothing, and reporting that as a pass would be worse than reporting
// nothing.
//
// scoreResult carries the raw streams alongside the verdict — not just the
// derived reason — because the archive (archive.go's archiveRun) keeps the
// scorer's own stdout/stderr verbatim beside each run, and re-deriving them
// from the reason string would lose whatever the scorer printed that was not
// the reason (a debug trace, intermediate jq output on a failed run).
type scoreResult struct {
	Passed  bool
	Reason  string // empty on a pass
	Stdout  []byte
	Stderr  []byte
	Verdict *Verdict // nil when the scorer wrote none
}

func score(ctx context.Context, fx Fixture, ws *workspace, transcriptPath, binDir, agentHome string) (scoreResult, error) {
	verdictPath, cleanup, err := newVerdictFile()
	if err != nil {
		return scoreResult{}, err
	}
	defer cleanup()

	cmd := exec.CommandContext(ctx, fx.ScorePath())
	cmd.Dir = fx.Dir
	cmd.Env = append(os.Environ(),
		"SR_EVAL_TRANSCRIPT="+transcriptPath,
		"SR_EVAL_PROJECT_DIR="+ws.project,
		"SR_EVAL_FIXTURE_DIR="+fx.Dir,
		"SR_EVAL_BIN_DIR="+binDir,
		"SR_EVAL_VERDICT_OUT="+verdictPath,
	)
	cmd.Env = append(cmd.Env, "SR_EVAL_AGENT_HOME="+agentHome)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	res := scoreResult{Stdout: []byte(stdout.String()), Stderr: []byte(stderr.String())}
	// Read whatever verdict the scorer wrote regardless of its exit code — a
	// scorer that fails may still have recorded exactly which check failed
	// and which passed, and that is the more useful archive of a failing run
	// than the reason string alone.
	res.Verdict = readVerdictFile(verdictPath)

	if runErr == nil {
		res.Passed = true
		return res, nil
	}

	if _, ok := runErr.(*exec.ExitError); !ok {
		// The script itself could not be run (not executable, missing
		// interpreter, timeout) — that is the "scorer could not run" case
		// the caller reports as exit 2, distinct from a script that ran and
		// said no.
		return res, runErr
	}

	res.Reason = scorerReason(stdout.String(), stderr.String())
	return res, nil
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
