package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// sr-eval archive records a project's Claude Code sessions into a git repository of its own.
// These tests run the real binaries (sr-eval, sr-session, sr-checks) against a project whose
// sessions the mock ran — real hooks wrote the state stores, real tracked ranges, real stored
// verdicts — and read the archive back.

const (
	judgeRule = "match: \"docs/**\"\nchecks:\n  - judge: ./rubric.md.j2\n"
	rubric    = "Does this change to the docs hold up?\n{{ change }}\n"
	failText  = "the date is not in the sources"
)

// world is a project with a committed judged `docs` rule whose judge refuses what it is asked
// about, so each session that commits a doc leaves a stored FAIL in the repository.
type world struct {
	e    *harness.Env
	proj string
	into string // the archive repository, outside everything else
}

func newWorld(t *testing.T) *world {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards())
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	e.CommitAll(proj, "the rule")
	e.InstallJudgeClaudeCapturing(proj, ".git/judge-prompt", `{"pass": false, "reasoning": "`+failText+`"}`)
	into, err := os.MkdirTemp("", "slop-archive-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(into) })
	return &world{e: e, proj: proj, into: into}
}

// session runs one session in the project that commits a doc; the doc's judge verdict is a
// stored FAIL, and the session's temp dir is left to the test.
func (w *world) session(t *testing.T, id string, extra ...harness.Turn) {
	t.Helper()
	w.sessionIn(t, w.proj, id, extra...)
}

func (w *world) sessionIn(t *testing.T, proj, id string, extra ...harness.Turn) {
	t.Helper()
	turns := append([]harness.Turn{
		harness.CommitFile("c-"+id, "docs/"+id+".md", "the release is Friday", "add "+id),
	}, extra...)
	w.e.Run(proj, id, "write the doc "+id, harness.Turns("done", turns...))
}

// background is a turn that starts a background task where the harness has them: the
// transcript then names the session's temp dir, and the task's output file is written there.
// Where it has not (Codex's and Cursor's receipts name no task) it is the same command run
// in the foreground, and keepsTempDir is false.
func background(t *testing.T, id string) harness.Turn {
	t.Helper()
	if keepsTempDir(t) {
		return harness.Background("bg-"+id, "Bash", map[string]string{"command": "echo background-" + id})
	}
	return harness.Bash("bg-"+id, "echo background-"+id)
}

// keepsTempDir: the harness keeps a per-session temp directory (scratchpad, task outputs)
// that an archive of the session holds. Declared by the capability that makes one exist.
func keepsTempDir(t *testing.T) bool {
	t.Helper()
	return harness.HasCap(t, harness.CapBackgroundTasks)
}

// tempDir is where Claude Code keeps the session's scratchpad and tasks, as the transcript names it.
func (w *world) tempDir(t *testing.T, id string) string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(w.e.TmpDir(), "claude-*", "*", id))
	if len(m) != 1 {
		t.Fatalf("want one temp dir for %s, got %v", id, m)
	}
	return m[0]
}

// id is the session's id as the harness names it, which is what the archive is asked for
// and what it names the session's directory by (a test's own id is an alias of it on
// harnesses that name their sessions themselves).
func (w *world) id(sess string) string { return w.e.HarnessSessionID(sess) }

// sessionArgs rewrites the test's session ids in an archive command line to the harness's.
func (w *world) sessionArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i := 0; i+1 < len(out); i++ {
		if out[i] == "--session" {
			out[i+1] = w.id(out[i+1])
		}
	}
	return out
}

// archiveEnv is the environment of an operator running sr-eval beside Claude Code: its config
// dir and temp root, and the sibling binaries on PATH.
func (w *world) archiveEnv() []string {
	return append(w.e.SessionEnv(""), "CLAUDE_CODE_TMPDIR="+w.e.TmpDir())
}

func (w *world) archiveIn(t *testing.T, cwd string, args ...string) harness.Result {
	t.Helper()
	return w.e.CLIDirectEnv(cwd, w.archiveEnv(), "sr-eval", append([]string{"archive"}, w.sessionArgs(args)...)...)
}

// archive runs it from the project and returns the entry directory it printed.
func (w *world) archive(t *testing.T, args ...string) string {
	t.Helper()
	r := w.archiveIn(t, w.proj, append(args, "--into", w.into)...)
	if r.Code != 0 {
		t.Fatalf("sr-eval archive %v: exit %d:\n%s", args, r.Code, r.Output)
	}
	lines := strings.Split(strings.TrimSpace(r.Output), "\n")
	dir := lines[len(lines)-1]
	if !strings.HasPrefix(dir, w.into) {
		t.Fatalf("the printed entry %q is not under %s:\n%s", dir, w.into, r.Output)
	}
	return dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

type manifest struct {
	Harness string `json:"harness"`
	Sources struct {
		Companions map[string]map[string]string `json:"companions"` // session -> item -> where, and how it was found
	} `json:"sources"`
	Subagents map[string]string `json:"subagents"` // session -> what became of its sub-agents
	Tools     map[string]string `json:"tool_versions"`
	Label     string            `json:"label"`
	Cwd       string            `json:"cwd"`
	Sessions  []string          `json:"sessions"`
	Checks    []struct {
		Repo     string   `json:"repo"`
		File     string   `json:"file"`
		Folders  []string `json:"folders"`
		Sessions []string `json:"tracked_by_sessions"`
	} `json:"checks"`
	Skipped []struct {
		Session string `json:"session"`
		Item    string `json:"item"`
		Reason  string `json:"reason"`
	} `json:"skipped"`
}

func readManifest(t *testing.T, dir string) manifest {
	t.Helper()
	var m manifest
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "archive.json"))), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// stateFiles lists the files of every state store the archive copied for a session, keyed by name.
func stateFiles(t *testing.T, dir, id string) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Join(dir, id, "state")
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out[rel] = p
		}
		return nil
	})
	return out
}
