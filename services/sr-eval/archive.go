package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// AppName is the directory sr-eval keeps its run archive under, matching the
// name sr-session already uses for its own XDG data dir.
const AppName = "sloprail"

// runRecord is the structured result of one fixture run, written as run.json
// alongside the archived transcripts. Field names are stable — a future
// `sr-eval report` (or any other consumer) reads this file, not stdout.
type runRecord struct {
	Fixture      string    `json:"fixture"`       // the fixture directory's own name (basename)
	FixtureDir   string    `json:"fixture_dir"`   // absolute path, for reproducing the run
	Model        string    `json:"model"`         // the resolved --model (fixture default or override)
	Harness      string    `json:"harness"`       // "claude-code" today; sr-agent's own resolution, once it reports one
	Passed       bool      `json:"passed"`
	Reason       string    `json:"reason"`         // empty on a pass
	AgentError   string    `json:"agent_error"`    // the agent-under-test's own exit error, if any — a run can still be scored after this
	Transcript   string    `json:"transcript"`     // the SOURCE path this run's transcript was copied from
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

// dataHome mirrors services/sr-session/statedir.go's own — the two packages
// cannot import each other (separate `package main`s; see go.mod's own
// reasoning: what passes between these binaries is exec, not import), so the
// small, stable part (the cross-platform XDG lookup) is duplicated rather than
// promoted to a shared internal/ package for one four-branch function.
func dataHome() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support"), nil
	case "windows":
		if dir := os.Getenv("LocalAppData"); dir != "" {
			return dir, nil
		}
		return filepath.Join(home, "AppData", "Local"), nil
	default:
		return filepath.Join(home, ".local", "share"), nil
	}
}

// ensureArchiveRepo makes sure root exists and is a git repository, creating
// both on first use. A plain directory of run folders would still be
// browsable, but git is what turns it into a history: `git log`, `git diff`
// between two runs' transcripts, `git blame` on when a fixture started
// failing — ordinary tools, no bespoke index to maintain.
func ensureArchiveRepo(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create archive root %s: %w", root, err)
	}
	if isDir(filepath.Join(root, ".git")) {
		return nil
	}
	init := exec.Command("git", "-C", root, "init", "--quiet")
	if out, err := init.CombinedOutput(); err != nil {
		return fmt.Errorf("git init %s: %w: %s", root, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// runID names one run's directory under <root>/<fixture>/: a sortable
// timestamp (so `ls` and `git log --oneline -- <fixture>` already read in
// run order) plus a short random suffix (so two runs started in the same
// second, e.g. two models run back to back by a script, never collide).
func runID(now time.Time) (string, error) {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix), nil
}

// archiveRun copies this run's transcripts and scorer output into the
// archive and commits them, then returns the run directory's absolute path.
//
// Copies, not moves: the transcript still belongs to Claude Code's own
// project directory (a real user's ordinary session history), and sr-eval
// has no business relocating it out from under whatever else might read it
// from there.
func archiveRun(rec runRecord, transcriptPath string, scoreStdout, scoreStderr []byte) (string, error) {
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
		subDir := filepath.Join(strings.TrimSuffix(transcriptPath, ".jsonl"), "subagents")
		if entries, err := os.ReadDir(subDir); err == nil && len(entries) > 0 {
			rec.HasSubagents = true
			if err := copyTree(subDir, filepath.Join(dir, "subagents")); err != nil {
				return "", fmt.Errorf("archive subagent transcripts: %w", err)
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

// commitRun stages and commits exactly this run's OWN directory — not `git
// add -A` over the whole archive, and not even the fixture's whole
// directory, so a run started while a SIBLING run for the same fixture is
// still being written (two models run back to back by a script) can never
// stage or commit that other run's half-written files.
//
// The repo's OWN configured identity and signing are used as-is — an
// operator who `git init`s this archive themselves and sets it up to match
// their own commits (name, email, commit.gpgsign, signingkey) elsewhere gets
// exactly that here too, sr-eval commits indistinguishable from their own.
// The placeholder identity below is a FALLBACK, not a default: only reached
// when the repo has no user.name/user.email configured anywhere (global or
// local) and `git commit` would otherwise refuse outright with "Author
// identity unknown" — a fresh archive on a fresh machine, before anyone has
// set it up. hasGitIdentity distinguishes the two cases; --no-gpg-sign is
// scoped to that same fallback path, so an operator's own signing
// configuration is never silently overridden.
func commitRun(root, runDir string, rec runRecord) error {
	relDir, err := filepath.Rel(root, runDir)
	if err != nil {
		return fmt.Errorf("resolve run dir relative to archive root: %w", err)
	}
	add := exec.Command("git", "-C", root, "add", "--", relDir)
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %w: %s", err, strings.TrimSpace(string(out)))
	}

	verdict := "pass"
	if !rec.Passed {
		verdict = "fail"
	}
	msg := fmt.Sprintf("%s: %s (%s)", rec.Fixture, verdict, rec.Model)

	args := []string{"-C", root}
	if !hasGitIdentity(root) {
		args = append(args,
			"-c", "user.name=sr-eval",
			"-c", "user.email=sr-eval@localhost",
		)
	}
	args = append(args, "commit", "--quiet", "-m", msg)
	if !hasGitIdentity(root) {
		// Only the placeholder identity's commits are forced unsigned — it
		// has no key to sign with, and letting `commit.gpgsign` (inherited
		// from a global config the archive itself never set) apply to it
		// would fail the commit outright over a key that names an identity
		// this fallback never claims to be.
		args = append(args, "--no-gpg-sign")
	}

	commit := exec.Command("git", args...)
	out, err := commit.CombinedOutput()
	if err != nil {
		// "nothing to commit" is not a real failure here: it means THIS
		// exact run (same fixture, same run id, same content) was already
		// archived, which only happens if archiveRun is called twice for
		// one run. Surfacing it as an error would make a caller's retry
		// logic worse, not safer.
		if strings.Contains(string(out), "nothing to commit") {
			return nil
		}
		return fmt.Errorf("git commit: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// hasGitIdentity reports whether `git -C root config user.email` resolves to
// anything — local, global, or system config all count, since `git commit`
// itself does not distinguish them. Checked once per commit rather than
// cached: this runs once per `sr-eval run`, not in a hot loop, and a cache
// would risk holding a stale answer across a run where the operator fixes
// their config mid-session.
func hasGitIdentity(root string) bool {
	out, err := exec.Command("git", "-C", root, "config", "user.email").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

