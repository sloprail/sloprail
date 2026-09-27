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
	c := exec.Command(filepath.Join(dir, script))
	c.Dir = dir
	c.Stdin = strings.NewReader(payload)
	c.Env = append(os.Environ(), "SR_WORKSPACE="+workspace, "SR_GUARDRAIL_DIR="+dir)
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

// T046_23: in a directory that is not a git repository, which lines are pinned
// cannot be decided — the predicate applies the citation (exit 0), never waives it.
func TestT046_23_PredicateOutsideARepoApplies(t *testing.T) {
	notRepo := t.TempDir()
	payload := `{"event":{"kind":"PreFileUpdate","path":"SPEC.md","resultKnown":true,` +
		`"oldContent":"a\nb\n","newContent":"a\nc\n","oldMarkers":[],"newMarkers":[]}}`
	if _, code := runRuleScript(t, ruleDir(t, "pinned-spec-holds"), "changes-pinned-lines.sh", notRepo, payload); code != 0 {
		t.Fatalf("outside a git repository the predicate exited %d; only a decided \"touches no pinned line\" may waive (exit 1)", code)
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
		{"waives", "#!/bin/sh\ncat >/dev/null\nexit 1\n", true},
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
	if out, code := runRuleScript(t, dir, "pin-still-matches-head.sh", t.TempDir(), payload); code != 0 {
		t.Errorf("the pin check refused a file with no invariant marker (exit %d): %s", code, out)
	}
	out, code := runRuleScript(t, dir, "pinned-text.sh", t.TempDir(), payload)
	var got struct {
		AdditionalContext struct {
			Pins []any `json:"pins"`
		} `json:"additionalContext"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || got.AdditionalContext.Pins == nil || len(got.AdditionalContext.Pins) != 0 {
		t.Errorf("the prepare did not hand the judge an empty pin list (exit %d): %s", code, out)
	}
}
