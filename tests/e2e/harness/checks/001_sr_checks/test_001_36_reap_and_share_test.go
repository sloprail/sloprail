package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

const treeScript = `#!/bin/sh
cat >/dev/null
echo "$SR_TREE" >>"$TREES"
`

// T001_36: a run reaps what killed runs left in the temp dir (a dead owner's, an old ownerless
// one), keeps a live owner's and a young ownerless one, and every rule of the run reads ONE
// checkout of head instead of making its own.
func TestT001_36_ARunReapsStaleTempDirsAndSharesOneCheckout(t *testing.T) {
	e, proj := session(t)
	trees := filepath.Join(t.TempDir(), "trees")
	for _, name := range []string{"alpha", "beta", "gamma"} {
		e.FileGuard(proj, name, "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n", map[string]string{"check.sh": treeScript})
	}
	base := e.CommitAll(proj, "the rules")
	e.WriteFile(proj, "docs/a.md", "hello\n")
	e.CommitAll(proj, "add a")

	tmp := t.TempDir()
	mk := func(name, owner string, age time.Duration) string {
		d := filepath.Join(tmp, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if owner != "" {
			if err := os.WriteFile(filepath.Join(d, "sr-snapshot-owner"), []byte(owner+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		old := time.Now().Add(-age)
		_ = os.Chtimes(d, old, old)
		return d
	}
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	deadOwned := mk("sr-test-case-dead", strconv.Itoa(dead.Process.Pid), time.Hour)
	live := mk("sr-test-case-live", strconv.Itoa(os.Getpid()), 48*time.Hour)
	oldLegacy := mk("sr-agent-output-old", "", 48*time.Hour)
	young := mk("sr-agent-output-young", "", time.Minute)

	cmd := exec.Command(e.BinPath("sr-checks"), "run", "--base", base, "--head", "HEAD")
	cmd.Dir = proj
	env := append(harness.HostEnv(), "HOME="+e.HomeDir(), "SLOP_SUBBIN_DIR="+e.BinDir())
	cmd.Env = append(append(env, e.SessionEnv(sessionID)...), "TMPDIR="+tmp, "TREES="+trees)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the run failed: %v\n%s", err, out)
	}

	for dir, wantGone := range map[string]bool{deadOwned: true, oldLegacy: true, live: false, young: false} {
		_, err := os.Stat(dir)
		if gone := os.IsNotExist(err); gone != wantGone {
			t.Errorf("%s: gone = %v, want %v", filepath.Base(dir), gone, wantGone)
		}
	}
	b, err := os.ReadFile(trees)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, l := range strings.Fields(string(b)) {
		seen[l] = true
	}
	if len(strings.Fields(string(b))) != 3 || len(seen) != 1 {
		t.Errorf("three rules must read one shared checkout, got %v", strings.Fields(string(b)))
	}
}
