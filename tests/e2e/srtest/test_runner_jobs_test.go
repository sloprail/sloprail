package e2e

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSrTestJobs: --jobs 1 runs the cases one at a time; with no flag at most 5 run at once (and 5 do).
func TestSrTestJobs(t *testing.T) {
	e := New(t)
	t.Run("jobs 1 is serial", func(t *testing.T) {
		root := t.TempDir()
		out := t.TempDir()
		for i := 0; i < 4; i++ {
			tcase(t, root, "s"+strconv.Itoa(i), fmt.Sprintf(`mkdir %q/lock || { echo "another case is running"; exit 1; }
sleep 0.3
rmdir %q/lock`, out, out))
		}
		got, res := runCases(t, e, root, "--jobs", "1")
		for s := range got {
			want(t, got, s, "pass")
		}
		if len(got) != 4 || res.Code != 0 {
			t.Errorf("%d results, exit %d:\n%s", len(got), res.Code, res.Output)
		}
	})
	t.Run("default is 5", func(t *testing.T) {
		root := t.TempDir()
		out := t.TempDir()
		for i := 0; i < 8; i++ {
			name := "d" + strconv.Itoa(i)
			tcase(t, root, name, fmt.Sprintf(`touch %q/running-%s
ls %q | grep -c '^running-' >> %q/counts
sleep 4
rm %q/running-%s`, out, name, out, out, out, name))
		}
		got, _ := runCases(t, e, root)
		if len(got) != 8 {
			t.Fatalf("%d results", len(got))
		}
		for s := range got {
			want(t, got, s, "pass")
		}
		maxSeen := 0
		for _, ln := range strings.Fields(readTrim(t, filepath.Join(out, "counts"))) {
			n, _ := strconv.Atoi(ln)
			maxSeen = max(maxSeen, n)
		}
		if maxSeen != 5 {
			t.Errorf("at most %d cases ran at once, want exactly 5", maxSeen)
		}
	})
}
