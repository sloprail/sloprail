package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// T049_25: a Changeset file missing a content field its status carries is undecidable,
// not an empty file. removes-content.sh applies the citation (exit 0) and
// skip-pure-addition.sh asks the judge, instead of reading the gap as "nothing removed".
// The control, a pure addition, still waives and skips.
func TestT049_25_AMissingContentFieldIsNotAPureAddition(t *testing.T) {
	guard := filepath.Join(repoRoot(t), "examples", "no-unasked-deletion", ".sloprail", "file-guard", "preserves-unasked-content")
	run := func(script, payload string) (string, int) {
		cmd := exec.Command("bash", filepath.Join(guard, script))
		cmd.Env = append(os.Environ(), "SR_GUARDRAIL_DIR="+guard)
		cmd.Stdin = strings.NewReader(payload)
		out, err := cmd.Output()
		var exit *exec.ExitError
		switch {
		case err == nil:
			return string(out), 0
		case errors.As(err, &exit):
			return string(out), exit.ExitCode()
		}
		t.Fatalf("run %s: %v", script, err)
		return "", -1
	}
	file := func(fields string) string {
		return `{"event":{"kind":"Changeset"},"changeset":{"files":[{"status":"M","path":"memories/x.md",` + fields + `}]}}`
	}
	const oldc = `"oldContent":"a\nb\n"`
	const newc = `"newContent":"a\nb\nc\n"`

	if _, code := run("removes-content.sh", file(oldc+","+newc)); code != 1 {
		t.Fatalf("control: a pure addition exited %d, want 1 (waived)", code)
	}
	if out, _ := run("skip-pure-addition.sh", file(oldc+","+newc)); !strings.Contains(out, `"skip": true`) {
		t.Fatalf("control: a pure addition was not skipped: %s", out)
	}
	for _, missing := range []string{oldc, newc} {
		if out, code := run("removes-content.sh", file(missing)); code != 0 {
			t.Errorf("removes-content.sh with %s only exited %d (%s), want 0 (applies)", missing, code, out)
		}
		if out, _ := run("skip-pure-addition.sh", file(missing)); strings.Contains(out, `"skip"`) {
			t.Errorf("skip-pure-addition.sh with %s only skipped the judge: %s", missing, out)
		}
	}
}
