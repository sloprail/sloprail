package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sloprail/sloprail/internal/harness"
)

// AppName is the directory sr-eval keeps its run archive under, matching the
// name sr-session already uses for its own XDG data dir.
const AppName = "sloprail"

// runRecord is the structured result of one fixture run, written as run.json
// alongside the archived transcripts. Field names are stable — a future
// `sr-eval report` (or any other consumer) reads this file, not stdout.
type runRecord struct {
	Fixture      string    `json:"fixture"`     // the fixture directory's own name (basename)
	FixtureDir   string    `json:"fixture_dir"` // absolute path, for reproducing the run
	Model        string    `json:"model"`       // the resolved --model (fixture default or override)
	Harness      string    `json:"harness"`     // the canonical id (claude, codex, cursor) the run was launched under
	Passed       bool      `json:"passed"`
	Reason       string    `json:"reason"`      // empty on a pass
	AgentError   string    `json:"agent_error"` // the agent-under-test's own exit error, if any — a run can still be scored after this
	Transcript   string    `json:"transcript"`  // the SOURCE path this run's transcript was copied from
	HasSubagents bool      `json:"has_subagents"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
}

// archiveRoot is the git-backed directory every run is recorded under:
// <XDG data dir>/sloprail/eval-runs, overridable for a caller (CI, a test)
// that wants its own.
func archiveRoot() (string, error) {
	if dir := os.Getenv("SLOPRAIL_EVAL_RUNS_DIR"); dir != "" {
		return dir, nil
	}
	home, err := dataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, AppName, "eval-runs"), nil
}

// archiveRun copies this run's transcripts and scorer output into the
// archive and commits them, then returns the run directory's absolute path.
//
// Copies, not moves: the transcript still belongs to Claude Code's own
// project directory (a real user's ordinary session history), and sr-eval
// has no business relocating it out from under whatever else might read it
// from there.
func archiveRun(rec runRecord, transcriptPath string, subagents []harness.SubagentFile, scoreStdout, scoreStderr []byte, verdict *Verdict) (string, error) {
	root, err := archiveRoot()
	if err != nil {
		return "", err
	}
	if err := ensureArchiveRepo(root); err != nil {
		return "", err
	}

	id, err := runID(rec.StartedAt)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, rec.Fixture, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create run dir %s: %w", dir, err)
	}

	if transcriptPath != "" {
		if err := copyFile(transcriptPath, filepath.Join(dir, "transcript.jsonl")); err != nil {
			return "", fmt.Errorf("archive transcript: %w", err)
		}
		if len(subagents) > 0 {
			rec.HasSubagents = true
			for _, f := range subagents {
				dst := filepath.Join(dir, "subagents", f.Rel)
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					return "", fmt.Errorf("archive subagent transcripts: %w", err)
				}
				if err := copyFile(f.Path, dst); err != nil {
					return "", fmt.Errorf("archive subagent transcripts: %w", err)
				}
			}
		}
	}

	scoreDir := filepath.Join(dir, "score")
	if err := os.MkdirAll(scoreDir, 0o755); err != nil {
		return "", fmt.Errorf("create score dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(scoreDir, "stdout.txt"), scoreStdout, 0o644); err != nil {
		return "", fmt.Errorf("write score stdout: %w", err)
	}
	if err := os.WriteFile(filepath.Join(scoreDir, "stderr.txt"), scoreStderr, 0o644); err != nil {
		return "", fmt.Errorf("write score stderr: %w", err)
	}
	if verdict != nil {
		// Layout mirrors a10n-eval's own scores/<scorer>/verdict.json beside
		// result.jsonl — the structured evidence lives next to the raw
		// streams, not instead of them.
		vBody, err := json.MarshalIndent(verdict, "", "  ")
		if err != nil {
			return "", fmt.Errorf("encode verdict.json: %w", err)
		}
		if err := os.WriteFile(filepath.Join(scoreDir, "verdict.json"), vBody, 0o644); err != nil {
			return "", fmt.Errorf("write verdict.json: %w", err)
		}
	}

	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode run.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.json"), body, 0o644); err != nil {
		return "", fmt.Errorf("write run.json: %w", err)
	}

	if err := commitRun(root, dir, rec); err != nil {
		return "", err
	}
	return dir, nil
}

// commitRun records one fixture run: commitDir with the run's verdict as the message.
func commitRun(root, runDir string, rec runRecord) error {
	verdict := "pass"
	if !rec.Passed {
		verdict = "fail"
	}
	return commitDir(root, runDir, fmt.Sprintf("%s: %s (%s)", rec.Fixture, verdict, rec.Model))
}
