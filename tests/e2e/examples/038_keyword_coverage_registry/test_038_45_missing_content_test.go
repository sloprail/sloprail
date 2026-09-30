package e2e

import (
	"errors"
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
		cmd.Env = append(os.Environ(), "SR_GUARDRAIL_DIR="+guard, "PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"))
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
		return `{"event":{"kind":"Changeset"},"changeset":{"files":[{"status":"M","path":"scanners/mine/scanner.yaml",` + fields + `}]}}`
	}
	const oldc = `"oldContent":"active: true\nkeywords:\n  - agent\n"`
	const newc = `"newContent":"active: true\nkeywords:\n  - agent\n  - cli\n"`

	if _, code := run(file(oldc + "," + newc)); code != 1 {
		t.Fatalf("control: an add-only change exited %d, want 1 (waived)", code)
	}
	if out, code := run(file(newc)); code != 0 {
		t.Errorf("an update with no oldContent exited %d (%s), want 0 (applies)", code, out)
	}
	if out, code := run(file(oldc)); code != 0 {
		t.Errorf("an update with no newContent exited %d (%s), want 0 (applies)", code, out)
	}
}
