package judgelimit

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if dir := os.Getenv("SR_SLOT_HOLDER_DIR"); dir != "" { // the child: holds the only slot, forever
		l := Limiter{Dir: dir, RunSlots: 1, Poll: 5 * time.Millisecond, Notify: time.Hour}
		release, err := l.AcquireRunSlot()
		if err != nil {
			os.Exit(3)
		}
		_ = os.WriteFile(filepath.Join(dir, "held"), nil, 0o644)
		time.Sleep(10 * time.Minute)
		release() // keeps the slot's file alive: a collected file would drop the lock
	}
	os.Exit(m.Run())
}

func startHolder(t *testing.T, dir string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "SR_SLOT_HOLDER_DIR="+dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "held")); err == nil {
			return cmd
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the holder never took the slot")
	return nil
}

// A slot is freed by the kernel when its holder is SIGKILLed: nothing to clean up, no stuck run.
func TestRunSlotIsFreedWhenItsHolderIsKilled(t *testing.T) {
	dir := t.TempDir()
	holder := startHolder(t, dir)
	l := Limiter{Dir: dir, RunSlots: 1, Poll: 5 * time.Millisecond, Notify: time.Hour, RunWait: 300 * time.Millisecond}
	if _, err := l.AcquireRunSlot(); !errors.Is(err, ErrWaitExpired) {
		t.Fatalf("a held slot must not be taken, got %v", err)
	}
	if err := holder.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()
	l.RunWait = 5 * time.Second
	start := time.Now()
	rel, err := l.AcquireRunSlot()
	if err != nil {
		t.Fatal(err)
	}
	rel()
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %s to get a slot its holder's death freed", time.Since(start))
	}
}

// The wait is bounded, and the error names who holds the slot.
func TestRunSlotWaitIsBoundedAndNamesTheHolder(t *testing.T) {
	dir := t.TempDir()
	startHolder(t, dir)
	l := Limiter{Dir: dir, RunSlots: 1, Poll: 5 * time.Millisecond, Notify: time.Hour, RunWait: 200 * time.Millisecond}
	_, err := l.AcquireRunSlot()
	if !errors.Is(err, ErrWaitExpired) || !strings.Contains(err.Error(), "held by pid") {
		t.Fatalf("want a bounded wait naming the holder, got %v", err)
	}
}
