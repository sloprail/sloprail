package e2e

// file_guard_judge_ran (examples/_shared/eval/trajectory-health.sh): did the
// file-guard's judge reach a verdict on THIS run's change to a file? The answer is
// read with `sr-checks show` from the project's results branch (the verdicts the
// engine stored), over the range the run's work made, and only when the rule's own
// changeset over that range holds the file; and a scorer that needs the judgement
// (no-unasked-deletion/remove-on-request) fails on anything but a found verdict.
// Each case has the real engine judge (the model is the mock's verdict) and runs
// the real shell.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// TestMain removes the binary build dir when this package's tests finish.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const judgeSession = "s-052-05"

type judgeFixture struct {
	t    *testing.T
	e    *harness.Env
	proj string // the project, a git repo with an origin and the rule installed
	data string // an empty data home: nothing the scorer reads lives there
}

// newJudgeFixture stands up a project with the file-guard preserves-unasked-content
// (one judge, whose prompt renders the changed files — a verdict is keyed by the prompt
// it was given, so a file that changed is a new question) installed and committed. skip makes the rule's prepare step abstain,
// as the shipped one does for a pure addition. withRepo false leaves a directory
// that is not a repository.
func newJudgeFixture(t *testing.T, withRepo bool, skip bool) *judgeFixture {
	t.Helper()
	if !withRepo {
		return &judgeFixture{t: t, proj: t.TempDir(), data: t.TempDir()}
	}
	e := harness.New(t)
	f := &judgeFixture{t: t, e: e, proj: e.Project(), data: t.TempDir()}
	e.GitInit(f.proj)
	dir := filepath.Join(".sloprail", "file-guard", "preserves-unasked-content")
	e.WriteFile(f.proj, filepath.Join(dir, "file-guard.yaml"),
		"match: 'path endsWith \".md\"'\nchecks:\n  - judge: ./rubric.md.j2\n    prepare: ./prepare.sh\n")
	e.WriteFile(f.proj, filepath.Join(dir, "rubric.md.j2"), "Is the change clean?\n{% for f in changeset.files %}<file path=\"{{ f.path }}\">\n{{ f.newContent }}\n</file>\n{% endfor %}")
	prepare := `printf '{"additionalContext": {}}\n'`
	if skip {
		prepare = `printf '{"skip": true}\n'`
	}
	e.WriteFile(f.proj, filepath.Join(dir, "prepare.sh"), "#!/bin/sh\ncat >/dev/null\n"+prepare+"\n")
	if err := os.Chmod(filepath.Join(f.proj, dir, "prepare.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.CommitAll(f.proj, "install the rule")
	e.WriteFile(f.proj, "a.md", "a\n")
	e.CommitAll(f.proj, "one")
	return f
}

// judge has the engine judge the range origin/main..HEAD as a session does, the model
// answering pass (or fail), and stores the verdict.
func (f *judgeFixture) judge(pass bool) {
	f.t.Helper()
	verdict := `{"pass": true, "reasoning": "clean"}`
	if !pass {
		verdict = `{"pass": false, "reasoning": "not clean"}`
	}
	f.e.InstallJudgeClaude(verdict)
	// The session has a transcript, as the agent's does: a refusal reached without one is
	// stored for no key (any check may read the transcript), so it would not be a verdict.
	tp := f.e.TranscriptPath(f.proj, judgeSession)
	if err := os.MkdirAll(filepath.Dir(tp), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(tp, []byte(`{"type":"user","uuid":"u1","message":{"role":"user","content":"work"}}`+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.e.CheckRunRaw(f.proj, judgeSession, "origin/main", "HEAD")
}

// ran runs file_guard_judge_ran for the file and returns "<JUDGE_RAN>". path is the
// PATH the scorer's shell sees; binDir the SR_EVAL_BIN_DIR a caller prepends to it.
func (f *judgeFixture) ran(file, path, binDir string) string {
	f.t.Helper()
	cmd := exec.Command("/bin/sh", "-c", `. "$SHARED/trajectory-health.sh"
file_guard_judge_ran preserves-unasked-content "$FILE"
printf '%s' "$JUDGE_RAN"`)
	cmd.Env = []string{"PATH=" + path, "HOME=" + f.data, "SHARED=" + sharedEval(f.t), "FILE=" + file,
		"SR_EVAL_PROJECT_DIR=" + f.proj, "SR_EVAL_BIN_DIR=" + binDir}
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("sh: %v\n%s", err, out)
	}
	return string(out)
}

// bin is the SR_EVAL_BIN_DIR of a fixture with an engine: where sr-checks is built.
func (f *judgeFixture) bin() string {
	if f.e == nil {
		return ""
	}
	return f.e.BinDir()
}

func TestT052_05_JudgeRan(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed; the scorer reports unknown (a failure) without it")
	}
	path := os.Getenv("PATH")
	for name, tc := range map[string]struct {
		skip bool
		file string
		run  func(f *judgeFixture)
		want string
	}{
		"a judge that passed":            {false, "a.md", func(f *judgeFixture) { f.judge(true) }, "yes"},
		"a judge that failed was judged": {false, "a.md", func(f *judgeFixture) { f.judge(false) }, "yes"},
		"a judge that was skipped":       {true, "a.md", func(f *judgeFixture) { f.judge(true) }, "no"},
		"nothing was ever judged":        {false, "a.md", func(f *judgeFixture) {}, "no"},
		"the verdict is about another file": {false, "b.md", func(f *judgeFixture) {
			f.judge(true)
		}, "no"},
		"the file changed after it was judged": {false, "a.md", func(f *judgeFixture) {
			f.judge(true)
			f.e.WriteFile(f.proj, "a.md", "a, then something else\n")
			f.e.CommitAll(f.proj, "two")
		}, "no"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newJudgeFixture(t, true, tc.skip)
			tc.run(f)
			if got := f.ran(tc.file, path, f.bin()); got != tc.want {
				t.Fatalf("JUDGE_RAN = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("no project directory", func(t *testing.T) {
		f := newJudgeFixture(t, false, false)
		if err := os.RemoveAll(f.proj); err != nil {
			t.Fatal(err)
		}
		if got := f.ran("a.md", path, ""); got != "unknown" {
			t.Fatalf("JUDGE_RAN = %q, want unknown", got)
		}
	})
	t.Run("no commit history", func(t *testing.T) {
		f := newJudgeFixture(t, false, false)
		if got := f.ran("a.md", path, ""); got != "unknown" {
			t.Fatalf("JUDGE_RAN = %q, want unknown", got)
		}
	})
	t.Run("no sr-checks", func(t *testing.T) {
		f := newJudgeFixture(t, true, false)
		f.judge(true)
		// A PATH holding only git and jq: the engine's binaries are not on it.
		tools := t.TempDir()
		for _, tool := range []string{"git", "jq"} {
			p, err := exec.LookPath(tool)
			if err != nil {
				t.Skipf("%s is not installed", tool)
			}
			if err := os.Symlink(p, filepath.Join(tools, tool)); err != nil {
				t.Fatal(err)
			}
		}
		if got := f.ran("a.md", tools, ""); got != "unknown" {
			t.Fatalf("JUDGE_RAN = %q, want unknown", got)
		}
	})
}
