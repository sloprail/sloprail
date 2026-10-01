package e2e

// file_guard_judge_ran (examples/_shared/eval/trajectory-health.sh): did the
// file-guard's judge reach a verdict on THIS run's change to a file? The answer is
// read from the run's checks.db, scoped to the project's own session, to a
// complete run whose head is a commit of the project's history, and to the file;
// and a scorer that needs the judgement (no-unasked-deletion/remove-on-request)
// fails on anything but a found verdict. Each case writes the rows the engine
// writes (the checkstore API) and runs the real shell.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/sessionpath"
)

const judgeKind = "check[0]:judge:./change-is-clean-and-absolute.md.j2"

type judgeFixture struct {
	t       *testing.T
	proj    string // the project, a git repo with one commit
	data    string // the data home the rows live under (XDG_DATA_HOME)
	head    string // the project's HEAD
	session string
}

func newJudgeFixture(t *testing.T, withRepo bool) *judgeFixture {
	t.Helper()
	f := &judgeFixture{t: t, proj: t.TempDir(), data: t.TempDir(), session: "sess-1"}
	if !withRepo {
		return f
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", f.proj, "-c", "user.email=e@e", "-c", "user.name=e"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(f.proj, "a.md"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "one")
	f.head = git("rev-parse", "HEAD")
	return f
}

// record writes one run of the rule with its checks, as the Stop evaluation does.
// complete false leaves the run RUNNING.
func (f *judgeFixture) record(session, head string, complete bool, checks ...checkstore.CheckRecord) {
	f.t.Helper()
	dir := filepath.Join(f.data, "sloprail", "sessions", sessionpath.EncodeWorkspace(f.proj), session)
	store, err := checkstore.Open(filepath.Join(dir, "checks.db"))
	if err != nil {
		f.t.Fatal(err)
	}
	defer store.Close()
	id, err := store.RecordRun(checkstore.CheckRun{
		RunIdentity: checkstore.RunIdentity{RepoID: "r", Branch: "main", SessionID: session},
		BatchID:     "b", CheckID: "file-guard/preserves-unasked-content", BaseRef: "base", HeadRef: head,
		Metadata: map[string]any{"state": "x"},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, c := range checks {
		if _, err := store.RecordCheck(id, c); err != nil {
			f.t.Fatal(err)
		}
	}
	if complete {
		if err := store.FinishRun(id); err != nil {
			f.t.Fatal(err)
		}
	}
}

func fileRow(file string) checkstore.CheckRecord {
	return checkstore.CheckRecord{Subject: file, Kind: "require:citation", Status: "pass"}
}

func judgeRow(status string) checkstore.CheckRecord {
	return checkstore.CheckRecord{Subject: "changeset", Kind: judgeKind, Status: status}
}

// ran runs file_guard_judge_ran for a.md and returns "<JUDGE_RAN>".
func (f *judgeFixture) ran(path string) string {
	f.t.Helper()
	cmd := exec.Command("/bin/sh", "-c", `. "$SHARED/trajectory-health.sh"
file_guard_judge_ran preserves-unasked-content a.md
printf '%s' "$JUDGE_RAN"`)
	cmd.Env = []string{"PATH=" + path, "HOME=" + f.data, "SHARED=" + sharedEval(f.t),
		"SR_EVAL_PROJECT_DIR=" + f.proj, "XDG_DATA_HOME=" + f.data}
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("sh: %v\n%s", err, out)
	}
	return string(out)
}

func TestT052_05_JudgeRan(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed; the scorer reports unknown (a failure) without it")
	}
	path := os.Getenv("PATH")
	for name, tc := range map[string]struct {
		run  func(f *judgeFixture)
		want string
	}{
		"a judge that passed":               {func(f *judgeFixture) { f.record("sess-1", f.head, true, fileRow("a.md"), judgeRow("pass")) }, "yes"},
		"a judge that failed was judged":    {func(f *judgeFixture) { f.record("sess-1", f.head, true, fileRow("a.md"), judgeRow("fail")) }, "yes"},
		"a judge that was skipped":          {func(f *judgeFixture) { f.record("sess-1", f.head, true, fileRow("a.md"), judgeRow("skip")) }, "no"},
		"no rows for the rule at all":       {func(f *judgeFixture) { f.record("sess-1", f.head, true, fileRow("a.md")) }, "no"},
		"the verdict is about another file": {func(f *judgeFixture) { f.record("sess-1", f.head, true, fileRow("b.md"), judgeRow("pass")) }, "no"},
		"the range is not this project's":   {func(f *judgeFixture) { f.record("sess-1", "deadbeef", true, fileRow("a.md"), judgeRow("pass")) }, "no"},
		"the run never finished":            {func(f *judgeFixture) { f.record("sess-1", f.head, false, fileRow("a.md"), judgeRow("pass")) }, "no"},
		"a run of a different session": {func(f *judgeFixture) {
			f.record("sess-1", f.head, true, fileRow("a.md"), judgeRow("skip"))
			f.recordForeign()
		}, "no"},
		"no check-results database": {func(f *judgeFixture) {}, "unknown"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newJudgeFixture(t, true)
			tc.run(f)
			if got := f.ran(path); got != tc.want {
				t.Fatalf("JUDGE_RAN = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("no project directory", func(t *testing.T) {
		f := newJudgeFixture(t, false)
		if err := os.RemoveAll(f.proj); err != nil {
			t.Fatal(err)
		}
		if got := f.ran(path); got != "unknown" {
			t.Fatalf("JUDGE_RAN = %q, want unknown", got)
		}
	})
	t.Run("no commit history", func(t *testing.T) {
		f := newJudgeFixture(t, false)
		if got := f.ran(path); got != "unknown" {
			t.Fatalf("JUDGE_RAN = %q, want unknown", got)
		}
	})
	t.Run("no sqlite3", func(t *testing.T) {
		f := newJudgeFixture(t, true)
		f.record("sess-1", f.head, true, fileRow("a.md"), judgeRow("pass"))
		if got := f.ran("/nonexistent"); got != "unknown" {
			t.Fatalf("JUDGE_RAN = %q, want unknown", got)
		}
	})
}

// recordForeign writes a passing, complete verdict for the file under a session
// directory whose runs carry ANOTHER session id: not this run's.
func (f *judgeFixture) recordForeign() {
	f.t.Helper()
	dir := filepath.Join(f.data, "sloprail", "sessions", sessionpath.EncodeWorkspace(f.proj), "sess-2")
	store, err := checkstore.Open(filepath.Join(dir, "checks.db"))
	if err != nil {
		f.t.Fatal(err)
	}
	defer store.Close()
	id, err := store.RecordRun(checkstore.CheckRun{
		RunIdentity: checkstore.RunIdentity{RepoID: "r", Branch: "main", SessionID: "someone-else"},
		BatchID:     "b", CheckID: "file-guard/preserves-unasked-content", BaseRef: "base", HeadRef: f.head,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, c := range []checkstore.CheckRecord{fileRow("a.md"), judgeRow("pass")} {
		if _, err := store.RecordCheck(id, c); err != nil {
			f.t.Fatal(err)
		}
	}
	if err := store.FinishRun(id); err != nil {
		f.t.Fatal(err)
	}
}
