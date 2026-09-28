package e2e

// The shipped scripts run directly, for the outcomes a scripted session cannot
// reach: a project that is not a git repository, a predicate that crashes, and a
// payload with no invariant markers. Each is run from its rule's folder with the
// environment the engine gives it (SR_WORKSPACE, SR_GUARDRAIL_DIR) and the check
// payload on stdin.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func ruleDir(t *testing.T, rule string) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "examples", "business-invariants", ".sloprail", "file-guard", rule)
}

// runRuleScript runs dir/script with payload on stdin and returns its stdout and
// exit code.
func runRuleScript(t *testing.T, dir, script, workspace, payload string) (string, int) {
	t.Helper()
	return runRuleScriptEnv(t, dir, script, workspace, payload)
}

// runRuleScriptEnv is runRuleScript with extra environment, later entries winning.
func runRuleScriptEnv(t *testing.T, dir, script, workspace, payload string, extra ...string) (string, int) {
	t.Helper()
	c := exec.Command(filepath.Join(dir, script))
	c.Dir = dir
	c.Stdin = strings.NewReader(payload)
	c.Env = append(append(os.Environ(), "SR_WORKSPACE="+workspace, "SR_GUARDRAIL_DIR="+dir), extra...)
	var out bytes.Buffer
	c.Stdout = &out
	err := c.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return out.String(), 0
	case errors.As(err, &exit):
		return out.String(), exit.ExitCode()
	default:
		t.Fatalf("run %s: %v", script, err)
		return "", -1
	}
}

// T046_23: a pin names <repo>@<sha>, so outside a git work tree nothing can be
// pinned — a decided answer: the predicate waives (exit 1, with its sentinel),
// rather than making every write in a git-less project need a citation. Without
// the git binary, whether a matched file is pinned cannot be told, and the
// citation applies (exit 0).
func TestT046_23_PredicateOutsideARepoWaives(t *testing.T) {
	notRepo := t.TempDir()
	payload := `{"event":{"kind":"PreFileUpdate","path":"SPEC.md","resultKnown":true,` +
		`"oldContent":"a\nb\n","newContent":"a\nc\n","oldMarkers":[],"newMarkers":[]}}`
	dir := ruleDir(t, "pinned-spec-holds")
	out, code := runRuleScript(t, dir, "changes-pinned-lines.sh", notRepo, payload)
	if code != 1 || !strings.Contains(out, `"waived"`) {
		t.Fatalf("outside a git work tree the predicate exited %d (%s); nothing can be pinned there, so it must waive", code, out)
	}

	// A PATH with the tools the script uses, but no git.
	bin := t.TempDir()
	for _, tool := range []string{"bash", "jq", "cat", "sed", "grep", "awk", "tr", "sort"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not found: %v", tool, err)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	c := exec.Command(filepath.Join(dir, "changes-pinned-lines.sh"))
	c.Dir = dir
	c.Stdin = strings.NewReader(payload)
	c.Env = []string{"PATH=" + bin, "SR_WORKSPACE=" + notRepo, "SR_GUARDRAIL_DIR=" + dir}
	err := c.Run()
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() == 1) {
		t.Fatalf("without git the predicate waived (%v); it cannot tell whether the file is pinned, so it must apply", err)
	}
}

// T046_24: the prepare skips the judge only when the predicate DECIDED the write
// touches nothing pinned (exit 1) — the same line the engine's `when` draws. A
// predicate that crashed (here exit 2) leaves the citation demanded, so skipping
// the judge would let any citation through.
func TestT046_24_PrepareRunsTheJudgeWhenThePredicateCrashes(t *testing.T) {
	dir := t.TempDir()
	prepare, err := os.ReadFile(filepath.Join(ruleDir(t, "pinned-spec-holds"), "only-when-pinned.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "only-when-pinned.sh"), prepare, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, predicate string
		skip            bool
	}{
		{"crashes", "#!/bin/sh\ncat >/dev/null\nexit 2\n", false},
		{"not-executable", "", false},
		{"applies", "#!/bin/sh\ncat >/dev/null\nexit 0\n", false},
		{"waives", "#!/bin/sh\ncat >/dev/null\necho '{\"waived\": \"nothing pinned\"}'\nexit 1\n", true},
		// exit 1 without the sentinel is a crash that happened to exit 1 (an
		// unbound variable under `set -u` in a sub-shell, a failed tool), not a
		// decided waiver.
		{"exit-1-without-sentinel", "#!/bin/sh\ncat >/dev/null\nexit 1\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			pred := filepath.Join(dir, "changes-pinned-lines.sh")
			mode := os.FileMode(0o755)
			body := c.predicate
			if body == "" {
				body, mode = "#!/bin/sh\nexit 1\n", 0o644
			}
			if err := os.WriteFile(pred, []byte(body), mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(pred, mode); err != nil {
				t.Fatal(err)
			}
			out, code := runRuleScript(t, dir, "only-when-pinned.sh", t.TempDir(), `{"event":{"kind":"PreFileUpdate","path":"SPEC.md"}}`)
			if code != 0 {
				t.Fatalf("the prepare exited %d: %s", code, out)
			}
			skipped := strings.Contains(out, `"skip"`)
			if skipped != c.skip {
				t.Fatalf("predicate %s: skip=%v, want %v (output %s)", c.name, skipped, c.skip, out)
			}
			if !c.skip && !strings.Contains(out, "additionalContext") {
				t.Fatalf("predicate %s: the judge got no context: %s", c.name, out)
			}
		})
	}
}

// T046_25: a payload with no invariant marker gives the pin check nothing to
// refuse and the prepare no pin to read. `seq 0 -1` counts DOWN on macOS, so a
// loop over it ran once with the fqn "null" and refused.
func TestT046_25_NoInvariantMarkersIsNothingToCheck(t *testing.T) {
	payload := `{"event":{"kind":"PostFileUpdate","path":"src/a.go","newMarkers":[{"kind":"endpoint","fqn":"x","line":1}]}}`
	dir := ruleDir(t, "pinned-invariant")
	// seq counting down, as macOS's does, first on PATH: GNU seq prints nothing
	// for `seq 0 -1`, so on a Linux runner the old loop would pass unnoticed.
	path := "PATH=" + bsdSeqDir(t) + string(os.PathListSeparator) + os.Getenv("PATH")
	if out, code := runRuleScriptEnv(t, dir, "pin-still-matches-head.sh", t.TempDir(), payload, path); code != 0 {
		t.Errorf("the pin check refused a file with no invariant marker (exit %d): %s", code, out)
	}
	out, code := runRuleScriptEnv(t, dir, "pinned-text.sh", t.TempDir(), payload, path)
	var got struct {
		AdditionalContext struct {
			Pins []any `json:"pins"`
		} `json:"additionalContext"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || got.AdditionalContext.Pins == nil || len(got.AdditionalContext.Pins) != 0 {
		t.Errorf("the prepare did not hand the judge an empty pin list (exit %d): %s", code, out)
	}
}

func writeExec(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// T046_35: the predicate runs on every write to a file the guard matches, twice
// (the `when`, then the prepare). A file that carries no marker and that no
// marker in the working tree or at HEAD names is answered by one fixed-string
// search per tree, with the payload parsed once — not by the full marker scan and
// a jq call per field.
func TestT046_35_AnUnpinnedFileIsAnsweredCheaply(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	writeExec(t, repo, "SPEC.md", "rules\n1. a\n")
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeExec(t, repo, "src/a.go", "// sr:invariant \""+repo+"@0000000:SPEC.md#L2-2\"\nfunc A() {}\n")
	if out, err := exec.Command("sh", "-c", "cd "+repo+" && git add -A && git commit -qm x --no-gpg-sign").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}

	// Shims that log each call, then run the real tool.
	shims, log := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	for _, tool := range []string{"git", "jq"} {
		real, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s: %v", tool, err)
		}
		writeExec(t, shims, tool, "#!/bin/sh\necho \""+tool+" $*\" >> "+log+"\nexec "+real+" \"$@\"\n")
	}
	payload := `{"event":{"kind":"PreFileUpdate","path":"specs/other.md","resultKnown":true,` +
		`"oldContent":"x\n","newContent":"y\n","oldMarkers":[],"newMarkers":[]}}`
	out, code := runRuleScriptEnv(t, ruleDir(t, "pinned-spec-holds"), "changes-pinned-lines.sh", repo, payload,
		"PATH="+shims+string(os.PathListSeparator)+os.Getenv("PATH"))
	if code != 1 || !strings.Contains(out, "waived") {
		t.Fatalf("an unpinned spec was not waived (exit %d): %s", code, out)
	}
	body, _ := os.ReadFile(log)
	calls := strings.Split(strings.TrimSpace(string(body)), "\n")
	jqCalls := 0
	for _, c := range calls {
		if strings.HasPrefix(c, "jq ") {
			jqCalls++
		}
		if strings.HasPrefix(c, "git ") && strings.Contains(c, " grep ") && strings.Contains(c, " -E ") {
			t.Errorf("an unpinned, unmarked file ran the full marker scan: %s", c)
		}
	}
	if jqCalls > 2 {
		t.Errorf("the payload was parsed %d times; once, plus the waiver's sentinel, is enough:\n%s", jqCalls, body)
	}
}

// T046_37: the pinned-invariant prepare skips the judge on a delete — a deleted
// file has no code left to rule on (whether it may drop its pins is
// pinned-spec-holds' question).
func TestT046_37_PrepareSkipsTheJudgeOnADelete(t *testing.T) {
	for _, kind := range []string{"PreFileDelete", "PostFileDelete"} {
		payload := `{"event":{"kind":"` + kind + `","path":"src/charge.go","oldContent":"x",` +
			`"oldMarkers":[{"kind":"invariant","fqn":"/r@abcdef1:SPEC.md#L1-1","line":1}]}}`
		out, code := runRuleScript(t, ruleDir(t, "pinned-invariant"), "pinned-text.sh", t.TempDir(), payload)
		if code != 0 || !strings.Contains(out, `"skip": true`) {
			t.Errorf("%s: the prepare did not skip the judge (exit %d): %s", kind, code, out)
		}
	}
}

// T046_53: without the pinned mock in .bin/ the mock-driven tests skip with the
// fix, rather than run against whatever mock is on PATH.
func TestT046_53_PinnedMockMissingSaysRunMakeMock(t *testing.T) {
	root := t.TempDir()
	writeExec(t, root, "x", "")
	if err := os.MkdirAll(filepath.Join(root, "tests", "e2e", "harness"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeExec(t, filepath.Join(root, "tests", "e2e", "harness"), "MOCK_VERSION", "v9.9.9\n")
	if msg := pinnedMockMissing(root, ""); !strings.Contains(msg, "run `make mock`") {
		t.Errorf("no stamp: %q, want the make mock advice", msg)
	}
	if msg := pinnedMockMissing(root, "/my/mock"); msg != "" {
		t.Errorf("A10N_CLAUDE_MOCK set: %q, want none", msg)
	}
	if err := os.MkdirAll(filepath.Join(root, ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeExec(t, filepath.Join(root, ".bin"), "a10n-claude-mock.v9.9.9", "")
	if msg := pinnedMockMissing(root, ""); msg != "" {
		t.Errorf("stamp present: %q, want none", msg)
	}
}

// T046_57: under CI a missing mock fails the test instead of skipping it, so a
// job without the mock cannot pass by running nothing.
func TestT046_57_MissingMockFailsUnderCI(t *testing.T) {
	ciRec := &recordingTB{}
	func() {
		defer func() { recover() }()
		missingMock(ciRec, "true", "no mock")
	}()
	if !ciRec.fatal || ciRec.skipped {
		t.Errorf("CI set: fatal=%v skipped=%v, want a failure", ciRec.fatal, ciRec.skipped)
	}
	devRec := &recordingTB{}
	func() {
		defer func() { recover() }()
		missingMock(devRec, "", "no mock")
	}()
	if !devRec.skipped || devRec.fatal {
		t.Errorf("CI unset: fatal=%v skipped=%v, want a skip", devRec.fatal, devRec.skipped)
	}
}

// recordingTB records Fatal and Skip instead of ending a test; each panics to
// stop the caller, as the real ones stop the goroutine.
type recordingTB struct {
	testing.TB
	fatal, skipped bool
}

func (r *recordingTB) Helper()           {}
func (r *recordingTB) Fatal(args ...any) { r.fatal = true; panic("fatal") }
func (r *recordingTB) Skip(args ...any)  { r.skipped = true; panic("skip") }
