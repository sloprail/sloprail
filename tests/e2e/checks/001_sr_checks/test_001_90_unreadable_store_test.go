package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// T001_90: a declaration store that cannot be READ is a refusal for `run` and `verify`, never
// zero guards and a green exit; no `.sloprail` at all stays exit 0.
// sr:proves fileguard/unloadable-guard-refuses
// sr:proves cli/checks-range-is-stated-and-resolves
// sr:proves cli/checks-exit-status-is-the-verdict
func TestT001_90_UnreadableDeclarationStoreRefuses(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions are not enforced for root")
	}
	e, proj := session(t)
	if r := checks(e, proj, "verify", "--base", "HEAD", "--head", "HEAD"); r.Code != 0 {
		t.Fatalf("no file-guard at all: exit %d:\n%s", r.Code, r.Output)
	}

	base := judged(t, e, proj, verdictPass)
	dir := filepath.Join(proj, ".sloprail", "file-guard")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	for _, cmd := range []string{"verify", "run"} {
		r := checks(e, proj, cmd, "--base", base, "--head", "HEAD")
		if r.Code == 0 {
			t.Fatalf("%s passed over an unreadable store:\n%s", cmd, r.Output)
		}
		contains(t, r.Output, "could not be read")
	}
}
