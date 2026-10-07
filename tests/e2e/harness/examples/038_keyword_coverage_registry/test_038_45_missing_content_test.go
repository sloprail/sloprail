package e2e

import (
	"errors"
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// T038_45: a Changeset file missing a content field its status carries is undecidable,
// not an empty file: drops-keywords.sh applies the citation (exit 0) instead of waiving
// (exit 1, "drops nothing"), and the control (an add-only change) still waives.
func TestT038_45_AMissingContentFieldIsNotADecidedNoDrop(t *testing.T) {
	guard := exampleFile(t, ".sloprail/file-guard/scanner-keywords-hold")
	stubs := t.TempDir()
	if err := os.WriteFile(filepath.Join(stubs, "sr-session"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(payload string) (string, int) {
		cmd := exec.Command("bash", filepath.Join(guard, "drops-keywords.sh"))
		cmd.Env = append(harness.HostEnv(), "SR_GUARDRAIL_DIR="+guard, "PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"))
		cmd.Stdin = strings.NewReader(payload)
		out, err := cmd.Output()
		var exit *exec.ExitError
		switch {
		case err == nil:
			return string(out), 0
		case errors.As(err, &exit):
			return string(out), exit.ExitCode()
		}
		t.Fatalf("run: %v", err)
		return "", -1
	}
	file := func(fields string) string {
		return `{"event":{"kind":"Changeset"},"subject":{"id":"scanners/mine/scanner.yaml","files":["scanners/mine/scanner.yaml"]},` +
			`"changeset":{"files":[{"status":"M","path":"scanners/mine/scanner.yaml",` + fields + `}]}}`
	}
	const oldc = `"oldContent":"active: true\nkeywords:\n  - agent\n"`
	const newc = `"newContent":"active: true\nkeywords:\n  - agent\n  - cli\n"`

	if _, code := run(file(oldc + "," + newc)); code != 1 {
		t.Fatalf("control: an add-only change exited %d, want 1 (waived)", code)
	}
	// No subject says nothing about which scanner to decide: undecidable, so it applies.
	if out, code := run(`{"event":{"kind":"Changeset"},"changeset":{"files":[{"status":"M","path":"scanners/mine/scanner.yaml",` + oldc + "," + newc + `}]}}`); code != 0 {
		t.Errorf("a payload with no subject exited %d (%s), want 0 (applies)", code, out)
	}
	// Only the subject's scanner is decided: another scanner dropping a keyword is context.
	elsewhere := `{"event":{"kind":"Changeset"},"subject":{"id":"scanners/mine/scanner.yaml","files":["scanners/mine/scanner.yaml"]},"changeset":{"files":[` +
		`{"status":"M","path":"scanners/other/scanner.yaml","oldContent":"active: true\nkeywords:\n  - agent\n  - cli\n","newContent":"active: true\nkeywords:\n  - agent\n"},` +
		`{"status":"M","path":"scanners/mine/scanner.yaml",` + oldc + "," + newc + `}]}}`
	if out, code := run(elsewhere); code != 1 {
		t.Errorf("another scanner's dropped keyword decided the subject: exited %d (%s), want 1 (waived)", code, out)
	}
	// A subject that matches no changed file decided nothing: apply, never waive.
	if out, code := run(`{"event":{"kind":"Changeset"},"subject":{"id":"scanners/z/scanner.yaml","files":["scanners/z/scanner.yaml"]},"changeset":{"files":[{"status":"M","path":"scanners/mine/scanner.yaml",` + oldc + "," + newc + `}]}}`); code != 0 {
		t.Errorf("a subject matching no changed file exited %d (%s), want 0 (applies)", code, out)
	}
	if out, code := run(file(newc)); code != 0 {
		t.Errorf("an update with no oldContent exited %d (%s), want 0 (applies)", code, out)
	}
	if out, code := run(file(oldc)); code != 0 {
		t.Errorf("an update with no newContent exited %d (%s), want 0 (applies)", code, out)
	}
}
