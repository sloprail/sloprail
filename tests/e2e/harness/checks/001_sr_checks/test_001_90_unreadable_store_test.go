package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// T001_90: a declaration store that cannot be READ is a refusal for `run` and `verify`, never
// zero guards and a green exit; no `.sloprail` at all stays exit 0.
//
// The store is made unreadable three ways: chmod 000 (which does not stop root, so that case
// runs for a non-root user only), a regular file where the file-guard directory should be, and
// a symlink loop. The last two hold for root too, so the test always checks something.
// sr:proves fileguard/unloadable-guard-refuses
// sr:proves cli/checks-range-is-stated-and-resolves
// sr:proves cli/checks-exit-status-is-the-verdict
func TestT001_90_UnreadableDeclarationStoreRefuses(t *testing.T) {
	breakers := map[string]func(t *testing.T, dir string){
		"a file where the directory should be": func(t *testing.T, dir string) {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dir, []byte("not a directory\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"a symlink loop": func(t *testing.T, dir string) {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Base(dir), dir); err != nil {
				t.Fatal(err)
			}
		},
	}
	if os.Geteuid() != 0 { // chmod 000 does not stop root
		breakers["chmod 000"] = func(t *testing.T, dir string) {
			if err := os.Chmod(dir, 0o000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		}
	}
	for name, breakStore := range breakers {
		t.Run(name, func(t *testing.T) {
			e, proj := session(t)
			if r := checks(e, proj, "verify", "--base", "HEAD", "--head", "HEAD"); r.Code != 0 {
				t.Fatalf("no file-guard at all: exit %d:\n%s", r.Code, r.Output)
			}

			base := judged(t, e, proj, verdictPass)
			breakStore(t, filepath.Join(proj, ".sloprail", "file-guard"))

			for _, cmd := range []string{"verify", "run"} {
				r := checks(e, proj, cmd, "--base", base, "--head", "HEAD")
				if r.Code == 0 {
					t.Fatalf("%s passed over an unreadable store:\n%s", cmd, r.Output)
				}
				contains(t, r.Output, "could not be read")
			}
		})
	}
}
