package judgelimit

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testLimiter(t *testing.T, slots int) Limiter {
	return Limiter{Dir: t.TempDir(), Slots: slots, Poll: 5 * time.Millisecond, Notify: 20 * time.Millisecond}
}

func TestSlotsAtMostNHolders(t *testing.T) {
	l := testLimiter(t, 3)
	var cur, peak int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := l.AcquireSlot()
			if err != nil {
				t.Error(err)
				return
			}
			n := atomic.AddInt32(&cur, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
					break
				}
			}
			time.Sleep(15 * time.Millisecond)
			atomic.AddInt32(&cur, -1)
			rel()
		}()
	}
	wg.Wait()
	if peak > 3 || peak < 1 {
		t.Fatalf("peak holders = %d, want 1..3", peak)
	}
}

func TestSlotWaiterSaysItWaits(t *testing.T) {
	var out bytes.Buffer
	l := testLimiter(t, 1)
	l.Out = &out
	rel, _ := l.AcquireSlot()
	go func() { time.Sleep(80 * time.Millisecond); rel() }()
	rel2, err := l.AcquireSlot()
	if err != nil {
		t.Fatal(err)
	}
	rel2()
	if !strings.Contains(out.String(), "waiting for a judge slot (1 busy)") {
		t.Fatalf("no waiting line: %q", out.String())
	}
}

// A process that dies releases its slot: the kernel drops its flock.
func TestSlotCrashReleases(t *testing.T) {
	if os.Getenv("JUDGELIMIT_HOLD") != "" {
		l := Limiter{Dir: os.Getenv("JUDGELIMIT_HOLD"), Slots: 1}
		if _, err := l.AcquireSlot(); err != nil {
			os.Exit(3)
		}
		os.Stdout.WriteString("held\n")
		time.Sleep(time.Minute)
		return
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestSlotCrashReleases")
	cmd.Env = append(os.Environ(), "JUDGELIMIT_HOLD="+dir)
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := stdout.Read(buf); err != nil {
		t.Fatal(err)
	}
	l := Limiter{Dir: dir, Slots: 1, Poll: 5 * time.Millisecond}
	if f, ok, _ := tryLock(filepath.Join(dir, "slots", "slot-0")); ok {
		unlock(f)()
		t.Fatal("the child's slot was not held")
	}
	cmd.Process.Kill()
	cmd.Wait()
	done := make(chan struct{})
	go func() {
		rel, _ := l.AcquireSlot()
		rel()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the slot of a killed process was never released")
	}
}

func TestInflightSecondWaiterWaitsForFirst(t *testing.T) {
	l := testLimiter(t, 1)
	rel, waited, err := l.AcquireInflight("k1", "x")
	if err != nil || waited {
		t.Fatalf("first: waited=%v err=%v", waited, err)
	}
	var verdict atomic.Value
	got := make(chan string, 1)
	go func() {
		rel2, w, err := l.AcquireInflight("k1", "x")
		if err != nil || !w {
			got <- "bad"
			return
		}
		defer rel2()
		got <- verdict.Load().(string) // what the first stored before it let go
	}()
	for i := 0; i < 200 && !l.Contended("k1"); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if !l.Contended("k1") {
		t.Fatal("the waiter left no marker")
	}
	verdict.Store("PASS")
	l.ClearContended("k1")
	rel()
	select {
	case v := <-got:
		if v != "PASS" {
			t.Fatalf("waiter saw %q", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter never got the lock")
	}
	// A different key is not blocked.
	r3, w3, _ := l.AcquireInflight("k2", "x")
	if w3 {
		t.Fatal("another key waited")
	}
	r3()
}

func TestRunLockSerialises(t *testing.T) {
	l := testLimiter(t, 1)
	r1, _ := l.AcquireRun("r", "x")
	var second atomic.Bool
	done := make(chan struct{})
	go func() { r2, _ := l.AcquireRun("r", "x"); second.Store(true); r2(); close(done) }()
	time.Sleep(50 * time.Millisecond)
	if second.Load() {
		t.Fatal("second run did not wait")
	}
	r1()
	<-done
}
