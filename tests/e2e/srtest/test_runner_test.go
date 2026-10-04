package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// result is one JSONL line of `sr-test run`.
type result struct {
	Subject  string `json:"subject"`
	Status   string `json:"status"`
	Output   string `json:"output"`
	Metadata struct {
		DurationMS int64             `json:"duration_ms"`
		Events     []json.RawMessage `json:"events"`
	} `json:"metadata"`
}

// results parses the result lines out of a run's output, keyed by subject. The harness merges stderr into
// the output, so any line that is not a result (the `--keep` notice) is left out.
func results(t *testing.T, out string) map[string]result {
	t.Helper()
	got := map[string]result{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var r result
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Subject != "" {
			got[strings.Replace(r.Subject, caseOwner+":", "", 1)] = r
		}
	}
	return got
}

// caseOwner is the rule every fixture case here sits under (a case belongs to a rule's folder); results
// are keyed by the case's name, the owner left off.
const caseOwner = "gate/g"

// casePath is where a case's test.sh lives under base (a project root, or a plugin folder); the
// layout of a case folder is spelled here and nowhere else in these tests.
func casePath(base, name string) string {
	return filepath.Join(base, ".sloprail", "gate", "g", "tests", name, "test.sh")
}

// tcase writes the project case <name> under root with a bash body.
func tcase(t *testing.T, root, name, body string) {
	t.Helper()
	write(t, root, casePath("", name), "#!/usr/bin/env bash\nset -euo pipefail\n"+body+"\n")
}

// runCases runs `sr-test run` on root and returns the parsed results and the raw run.
func runCases(t *testing.T, e *harness.Env, root string, args ...string) (map[string]result, harness.Result) {
	t.Helper()
	res := e.CLIDirect(root, "sr-test", append([]string{"run"}, args...)...)
	return results(t, res.Output), res
}

func want(t *testing.T, got map[string]result, subject, status string) result {
	t.Helper()
	r, ok := got[subject]
	if !ok {
		t.Fatalf("no result for %q in %v", subject, got)
	}
	if r.Status != status {
		t.Errorf("%s: status %q, want %q (output: %s)", subject, r.Status, status, r.Output)
	}
	return r
}

// TestSrTestStatusAndExitCode: exit 0 is pass, 1 (or any other non-zero) is fail, 2 is error; a failing
// case carries its output, a passing one none, and `sr-test run` exits non-zero when any case did not pass
// and zero when every one did.
func TestSrTestStatusAndExitCode(t *testing.T) {
	e := New(t)
	root := t.TempDir()
	tcase(t, root, "a-pass", "echo quiet")
	tcase(t, root, "b-fail", "echo the-assertion-that-failed >&2; exit 1")
	tcase(t, root, "c-error", "echo the-setup-that-broke >&2; exit 2")
	tcase(t, root, "d-other", "exit 7")
	got, res := runCases(t, e, root)
	if res.Code == 0 {
		t.Errorf("sr-test run exited 0 with failing cases:\n%s", res.Output)
	}
	if r := want(t, got, "a-pass", "pass"); r.Output != "" {
		t.Errorf("a passing case carries output %q", r.Output)
	}
	if r := want(t, got, "b-fail", "fail"); !strings.Contains(r.Output, "the-assertion-that-failed") {
		t.Errorf("b-fail output %q", r.Output)
	}
	if r := want(t, got, "c-error", "error"); !strings.Contains(r.Output, "the-setup-that-broke") {
		t.Errorf("c-error output %q", r.Output)
	}
	want(t, got, "d-other", "fail")

	// all pass: exit 0
	ok := t.TempDir()
	tcase(t, ok, "only", "true")
	if got, res := runCases(t, e, ok); res.Code != 0 {
		t.Errorf("sr-test run exited %d with every case passing:\n%s", res.Code, res.Output)
	} else {
		want(t, got, "only", "pass")
	}
}

// TestSrTestTimeoutKillsTheCase: a case that outlives --timeout is an error saying so, and the
// processes it started (its whole process group) are killed with it.
func TestSrTestTimeoutKillsTheCase(t *testing.T) {
	e := New(t)
	root := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	tcase(t, root, "hangs", fmt.Sprintf("sleep 300 & echo $! > %q\necho started-and-waiting\nwait", pidFile))
	tcase(t, root, "quick", "true")
	started := time.Now()
	got, res := runCases(t, e, root, "--timeout", "2s")
	if time.Since(started) > 60*time.Second {
		t.Errorf("the run took %s; the timeout did not stop the case", time.Since(started))
	}
	r := want(t, got, "hangs", "error")
	if !strings.Contains(r.Output, "timed out after 2s") || !strings.Contains(r.Output, "started-and-waiting") {
		t.Errorf("the timeout is not reported with the case's output: %q", r.Output)
	}
	want(t, got, "quick", "pass")
	if res.Code == 0 {
		t.Errorf("a timed-out case did not fail the run")
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the case never started its child: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the case's child process %d outlived the timeout", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestSrTestKeep: without --keep the case's temp dir is gone after the run; with --keep it stays, its
// path is printed on stderr beside the subject, and it holds what the case worked in.
func TestSrTestKeep(t *testing.T) {
	e := New(t)
	where := t.TempDir()
	root := t.TempDir()
	tcase(t, root, "where", fmt.Sprintf(`echo hello > ./made-by-the-case
dirname "$PWD" > %q`, filepath.Join(where, "dir")))

	got, _ := runCases(t, e, root)
	want(t, got, "where", "pass")
	gone := readTrim(t, filepath.Join(where, "dir"))
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Errorf("without --keep the temp dir %s is still there (%v)", gone, err)
	}

	got, res := runCases(t, e, root, "--keep")
	want(t, got, "where", "pass")
	kept := readTrim(t, filepath.Join(where, "dir"))
	t.Cleanup(func() { os.RemoveAll(kept) })
	m := regexp.MustCompile(`sr-test: kept (\S+) \(` + caseOwner + `:where\)`).FindStringSubmatch(res.Output)
	if m == nil {
		t.Fatalf("--keep printed no `kept <dir> (<subject>)` line:\n%s", res.Output)
	}
	// the printed path and the case's own view of it name the same directory (macOS /var is a symlink)
	if a, b := realpath(t, m[1]), realpath(t, kept); a != b {
		t.Errorf("--keep printed %s, the case ran in %s", a, b)
	}
	for _, p := range []string{"project/made-by-the-case", "project/.git", "case/test.sh", "home", "events.jsonl"} {
		if _, err := os.Stat(filepath.Join(m[1], p)); err != nil {
			t.Errorf("kept dir lacks %s: %v", p, err)
		}
	}
}

// TestSrTestEventsReachTheResult: the lines the case appends to SR_EVENTS_FILE are the result's events,
// in order; a line that is not JSON is left out.
func TestSrTestEventsReachTheResult(t *testing.T) {
	e := New(t)
	root := t.TempDir()
	tcase(t, root, "emits", `echo '{"kind":"GateChecked","rule":"r","n":1}' >> "$SR_EVENTS_FILE"
echo 'this is not json' >> "$SR_EVENTS_FILE"
echo '{"kind":"Stop","n":2}' >> "$SR_EVENTS_FILE"`)
	tcase(t, root, "silent", "true")
	got, _ := runCases(t, e, root)
	var kinds []string
	for _, raw := range want(t, got, "emits", "pass").Metadata.Events {
		var ev struct {
			Kind string
			N    int
		}
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, ev.Kind+":"+strconv.Itoa(ev.N))
	}
	if strings.Join(kinds, ",") != "GateChecked:1,Stop:2" {
		t.Errorf("events %v", kinds)
	}
	if n := len(want(t, got, "silent", "pass").Metadata.Events); n != 0 {
		t.Errorf("a case that emitted nothing has %d events", n)
	}
}

// TestSrTestWorkspaceIsolation: each case starts in a fresh empty git repository of its own with a fixed
// test identity, a fake HOME, and its own folder outside the project. A case may still `git init` again or
// override the identity.
func TestSrTestWorkspaceIsolation(t *testing.T) {
	e := New(t)
	root := t.TempDir()
	const ident = "sr-test <sr-test@sloprail.invalid>"
	write(t, root, ".sloprail/rules-marker.txt", "the rules")
	// the project case: a repository, nothing to commit, rules copied in, its tests not
	tcase(t, root, "project", `test "$(git rev-parse --is-inside-work-tree)" = true
test "$(git rev-parse --show-toplevel)" = "$(pwd -P)"
test -z "$(git status --porcelain --untracked-files=all | grep -v '^?? .sloprail/')"
test -z "$(git log --oneline 2>/dev/null)"
test -f .sloprail/rules-marker.txt
test -z "$(find .sloprail -name test.sh)"
test "$(ls -A | tr '\n' ' ')" = ".git .sloprail "`)
	// the commit identity with no git config of the case's own
	tcase(t, root, "identity", `git commit -q --allow-empty -m x
test "$(git log -1 --format='%an <%ae>|%cn <%ce>')" = "`+ident+`|`+ident+`"`)
	// HOME is a fake directory beside the project, not the caller's
	tcase(t, root, "home", `[ "$(cd -P "$HOME" && pwd -P)" = "$(dirname "$(pwd -P)")/home" ] || { echo "HOME=$HOME PWD=$PWD"; exit 1; }
[ -d "$HOME" ] || { echo "no HOME dir"; exit 1; }
[ -z "$(ls -A "$HOME")" ] || { echo "HOME holds: $(ls -A "$HOME")"; exit 1; }
[ ! -e "$HOME/.gitconfig" ]`)
	// the case's folder is beside the project, not in it, so the project stays clean for a Stop hook
	tcase(t, root, "case-dir", `test -f "$SR_TEST_CASE_DIR/test.sh"
case "$SR_TEST_CASE_DIR/" in "$(pwd -P)"/*|"$PWD"/*) echo "case dir inside the project: $SR_TEST_CASE_DIR"; exit 1;; esac
case "$SR_EVENTS_FILE" in "$(pwd -P)"/*|"$PWD"/*) echo "events file inside the project"; exit 1;; esac
test -z "$(git status --porcelain --untracked-files=all | grep -v '^?? .sloprail/')"`)
	// the existing way still works: git init again and a commit with -c identity (the runner's
	// GIT_AUTHOR_* is what git takes first, so the author stays the test identity)
	tcase(t, root, "reinit", `git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m x
test "$(git rev-list --count HEAD)" = 1`)
	tcase(t, root, "override", `export GIT_AUTHOR_NAME=other GIT_AUTHOR_EMAIL=other@example.test
git commit -q --allow-empty -m x
test "$(git log -1 --format='%an <%ae>|%cn <%ce>')" = "other <other@example.test>|`+ident+`"`)
	// a plugin case: nothing copied, still a repository with the identity
	write(t, root, "mkt/plugins/p/.claude-plugin/plugin.json", `{"name":"p"}`)
	write(t, root, casePath("mkt/plugins/p", "plugin"), `#!/usr/bin/env bash
set -euo pipefail
test ! -e .sloprail
git commit -q --allow-empty -m x
test "$(git log -1 --format='%an <%ae>')" = "sr-test <sr-test@sloprail.invalid>"
`)

	got, res := runCases(t, e, root)
	for _, s := range []string{"project", "identity", "home", "case-dir", "reinit", "override", "mkt/plugins/p:plugin"} {
		want(t, got, s, "pass")
	}
	if res.Code != 0 {
		t.Errorf("exit %d:\n%s", res.Code, res.Output)
	}
}

// TestSrTestAmbientHomeAndSessionDoNotLeak: the caller's HOME and its Claude Code session never reach a
// case, so it passes the same on a machine and in CI.
func TestSrTestAmbientHomeAndSessionDoNotLeak(t *testing.T) {
	e := New(t)
	root := t.TempDir()
	tcase(t, root, "clean", `test -z "${CLAUDE_CODE_SESSION_ID:-}"
test -z "${SR_FROM_THE_CALLER:-}"
test "$HOME" != /nonexistent-caller-home
test "$(cd -P "$HOME" && pwd -P)" = "$(dirname "$(pwd -P)")/home"`)
	res := e.CLIDirectEnv(root, []string{"CLAUDE_CODE_SESSION_ID=ambient-session", "SR_FROM_THE_CALLER=1", "HOME=/nonexistent-caller-home"}, "sr-test", "run")
	want(t, results(t, res.Output), "clean", "pass")
}

// judgeCase is a case that sets up a file-guard with a judge in the project, commits a change it
// matches and runs `sr-checks run` over it; the judge's outcome is the FileGuardChecked event. mocks is the
// SR_CHECKS_JUDGE_MOCKS the case exports first ("" leaves what sr-test set).
func judgeCase(mocks string) string {
	return mocks + `
mkdir -p .sloprail/file-guard/j
printf 'match: "*.txt"\nchecks:\n  - judge: ./j.md.j2\n' > .sloprail/file-guard/j/file-guard.yaml
printf '<change>\n{{ change }}\n</change>\n' > .sloprail/file-guard/j/j.md.j2
git add -A && git commit -q -m rules
BASE=$(git rev-parse HEAD)
echo hello > a.txt
git add -A && git commit -q -m change
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 || true`
}

// judgeEvent is the one FileGuardChecked event of the rule j in a result.
func judgeEvent(t *testing.T, r result) (outcome, reason string) {
	t.Helper()
	var found []string
	for _, raw := range r.Metadata.Events {
		var ev struct{ Kind, Rule, Outcome, Reason string }
		if json.Unmarshal(raw, &ev) == nil && ev.Kind == "FileGuardChecked" && strings.HasSuffix(ev.Rule, "j") {
			found = append(found, ev.Outcome)
			outcome, reason = ev.Outcome, ev.Reason
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one FileGuardChecked event for the rule j, got %v in %s", found, r.Metadata.Events)
	}
	return outcome, reason
}

// TestSrTestJudges: with no judge mock the judge is an error that names the missing entry and never reaches
// a model, quickly; with a mock, the mock's verdict (pass or refusal with its reasoning) is the check's.
func TestSrTestJudges(t *testing.T) {
	e := New(t)
	root := t.TempDir()
	mockDir := t.TempDir()
	write(t, mockDir, "no.sh", `#!/usr/bin/env bash
cat >/dev/null
echo '{"pass":false,"reasoning":"the-mock-refuses-this"}'
`)
	write(t, mockDir, "yes.sh", `#!/usr/bin/env bash
cat >/dev/null
echo '{"pass":true,"reasoning":"the-mock-allows-this"}'
`)
	mock := func(script string) string {
		return `export SR_CHECKS_JUDGE_MOCKS='{"file-guard/j/j":"` + filepath.Join(mockDir, script) + `"}'`
	}
	tcase(t, root, "unmocked", judgeCase(""))
	tcase(t, root, "mocked-refuses", judgeCase(mock("no.sh")))
	tcase(t, root, "mocked-passes", judgeCase(mock("yes.sh")))
	tcase(t, root, "other-judge-mocked", judgeCase(`export SR_CHECKS_JUDGE_MOCKS='{"file-guard/not-j/j":"`+filepath.Join(mockDir, "yes.sh")+`"}'`))

	started := time.Now()
	got, _ := runCases(t, e, root)
	if d := time.Since(started); d > 90*time.Second {
		t.Errorf("the run took %s: a judge did not fail fast", d)
	}
	for _, n := range []string{"unmocked", "other-judge-mocked"} {
		out, reason := judgeEvent(t, want(t, got, n, "pass"))
		// the check could not decide, which the engine refuses on (never reads as approval)
		if out != "refused" || !strings.Contains(reason, `has no entry in SR_CHECKS_JUDGE_MOCKS`) || !strings.Contains(reason, "never sent to a model") {
			t.Errorf("%s: outcome %q reason %q, want a refusal naming the missing mock", n, out, reason)
		}
	}
	if out, reason := judgeEvent(t, want(t, got, "mocked-refuses", "pass")); out != "refused" || !strings.Contains(reason, "the-mock-refuses-this") {
		t.Errorf("mocked-refuses: outcome %q reason %q", out, reason)
	}
	if out, _ := judgeEvent(t, want(t, got, "mocked-passes", "pass")); out != "passed" {
		t.Errorf("mocked-passes: outcome %q", out)
	}
}

// TestSrTestLiveJudgesLeavesTheMocksUnset: by default a case has SR_CHECKS_JUDGE_MOCKS={}; with
// --live-judges the variable is not set at all, so a judge may reach a model.
func TestSrTestLiveJudgesLeavesTheMocksUnset(t *testing.T) {
	e := New(t)
	root := t.TempDir()
	tcase(t, root, "mocks", `test "${SR_CHECKS_JUDGE_MOCKS-UNSET}" = "{}"`)
	got, _ := runCases(t, e, root)
	want(t, got, "mocks", "pass")

	live := t.TempDir()
	tcase(t, live, "mocks", `test "${SR_CHECKS_JUDGE_MOCKS-UNSET}" = UNSET`)
	got, _ = runCases(t, e, live, "--live-judges")
	want(t, got, "mocks", "pass")
}

// TestSrTestParallelCasesDoNotShareAWorkspace: cases running at the same time (each waits until every
// one has started) each get their own project, HOME, case folder and events file.
func TestSrTestParallelCasesDoNotShareAWorkspace(t *testing.T) {
	e := New(t)
	root := t.TempDir()
	out := t.TempDir()
	const n = 6
	for i := 0; i < n; i++ {
		name := "c" + strconv.Itoa(i)
		tcase(t, root, name, fmt.Sprintf(`OUT=%q
touch "$OUT/started-%s"
for _ in $(seq 1 200); do
  [ "$(ls "$OUT" | grep -c '^started-')" -ge %d ] && break
  sleep 0.1
done
test "$(ls "$OUT" | grep -c '^started-')" -ge %d || { echo "the cases did not run at the same time"; exit 1; }
echo "$(pwd -P)|$HOME|$SR_TEST_CASE_DIR|$SR_EVENTS_FILE" > "$OUT/where-%s"
touch "mine-%s"
test "$(ls | grep -c '^mine-')" = 1 || { echo "another case's files are in this project"; exit 1; }
echo '{"kind":"X","case":"%s"}' >> "$SR_EVENTS_FILE"
git commit -q --allow-empty -m "%s"
test "$(git rev-list --count HEAD)" = 1`, out, name, n, n, name, name, name, name))
	}
	got, res := runCases(t, e, root, "--jobs", strconv.Itoa(n))
	seen := map[string]string{}
	for i := 0; i < n; i++ {
		name := "c" + strconv.Itoa(i)
		r := want(t, got, name, "pass")
		parts := strings.Split(readTrim(t, filepath.Join(out, "where-"+name)), "|")
		if len(parts) != 4 {
			t.Fatalf("%s: %v", name, parts)
		}
		for k, p := range parts {
			key := []string{"project", "home", "case dir", "events file"}[k] + " " + p
			if other, dup := seen[key]; dup {
				t.Errorf("%s and %s share a %s", name, other, key)
			}
			seen[key] = name
		}
		if len(r.Metadata.Events) != 1 || !strings.Contains(string(r.Metadata.Events[0]), `"case":"`+name+`"`) {
			t.Errorf("%s: events %s", name, r.Metadata.Events)
		}
	}
	if res.Code != 0 {
		t.Errorf("exit %d:\n%s", res.Code, res.Output)
	}
}

// alive reports whether pid is a running process; a zombie (killed, not yet reaped) is dead.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	st := strings.TrimSpace(string(out))
	return st != "" && !strings.HasPrefix(st, "Z")
}

func readTrim(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func realpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
