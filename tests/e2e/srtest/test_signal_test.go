package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// TestSrTestSignalKillsTheCasesGroups: SIGTERM and SIGINT to `sr-test run` end the case it is
// running and the processes that case started, instead of orphaning them (#273).
func TestSrTestSignalKillsTheCasesGroups(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		e := New(t)
		root := t.TempDir()
		pids := filepath.Join(t.TempDir(), "pids")
		tcase(t, root, "hangs", fmt.Sprintf("echo $$ >> %q\nsleep 300 &\necho $! >> %q\nwait", pids, pids))

		cmd := exec.Command(e.BinPath("sr-test"), "run")
		cmd.Dir = root
		cmd.Env = append(harness.HostEnv(), "HOME="+e.HomeDir(), "SLOP_SUBBIN_DIR="+e.BinDir())
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		var running []int
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) && len(running) < 2 {
			running = running[:0]
			b, _ := os.ReadFile(pids)
			for _, f := range strings.Fields(string(b)) {
				if n, err := strconv.Atoi(f); err == nil {
					running = append(running, n)
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(running) < 2 {
			_ = cmd.Process.Kill()
			t.Fatalf("%s: the case never started its processes", sig)
		}
		_ = cmd.Process.Signal(sig)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
			t.Fatalf("%s: sr-test did not exit", sig)
		}
		time.Sleep(200 * time.Millisecond)
		for _, p := range running {
			if syscall.Kill(p, 0) == nil {
				_ = syscall.Kill(p, syscall.SIGKILL)
				t.Errorf("%s: pid %d outlived sr-test", sig, p)
			}
		}
	}
}
