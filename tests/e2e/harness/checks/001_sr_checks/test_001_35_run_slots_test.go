package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The machine runs only a few `sr-checks run` at once (#273): a run is a fan-out of git and shell
// processes, so N agents each running one multiplied into hundreds of runnable processes. The
// rest queue for a slot, and none of them fails.

const slotScript = `#!/bin/sh
cat >/dev/null
echo s >>"$LEDGER"
sleep 2
echo e >>"$LEDGER"
`

// T001_35: four runs against two run slots: at most two check scripts are ever running, and all
// four runs finish and pass.
func TestT001_35_RunsQueueForAHostWideSlot(t *testing.T) {
	shared := t.TempDir()
	ledger := filepath.Join(shared, "ledger")
	type run struct {
		e    *Env
		proj string
		base string
	}
	var runs []run
	for i := 0; i < 4; i++ {
		e, proj := session(t)
		e.FileGuard(proj, "slow", "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n", map[string]string{"check.sh": slotScript})
		base := e.CommitAll(proj, "the rule")
		e.WriteFile(proj, "docs/a.md", "hello\n")
		e.CommitAll(proj, "add a")
		runs = append(runs, run{e, proj, base})
	}
	extra := []string{"LEDGER=" + ledger, "SLOPRAIL_LOCK_DIR=" + filepath.Join(shared, "locks"), "SLOPRAIL_RUN_SLOTS=2"}
	cmds := make([]*exec.Cmd, 0, len(runs))
	for _, r := range runs {
		cmd := exec.Command(r.e.BinPath("sr-checks"), "run", "--base", r.base, "--head", "HEAD")
		cmd.Dir = r.proj
		env := append(harness.HostEnv(), "HOME="+r.e.HomeDir(), "SLOP_SUBBIN_DIR="+r.e.BinDir())
		cmd.Env = append(append(env, r.e.SessionEnv(sessionID)...), extra...)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
	}
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Errorf("run %d did not pass: %v", i, err)
		}
	}

	b, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	cur, peak := 0, 0
	for _, ev := range strings.Fields(string(b)) {
		if ev == "s" {
			cur++
		} else {
			cur--
		}
		peak = max(peak, cur)
	}
	if peak != 2 {
		t.Fatalf("at most 2 runs may be inside a check at once, and 2 are expected to be; peak = %d\n%s", peak, b)
	}
}
