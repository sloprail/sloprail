// Package judgelimit coordinates parallel `sr-checks run` processes on one machine through lock
// files under the user cache dir. Every lock is an flock: the kernel drops it when the holder
// exits or crashes, so nothing needs cleaning up after a killed run.
//
//   - A counting semaphore (Slots files, default 8) bounds how many judges run at once across all
//     processes: a process waits for a free slot, it never fails.
//   - An in-flight lock per verdict key lets the second process judging the same key wait for the
//     first and read its verdict instead of judging again.
//   - A run lock serialises two runs of the same worktree and range.
package judgelimit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// SlotsEnv overrides how many judges may run at once on the machine.
	SlotsEnv = "SLOPRAIL_JUDGE_SLOTS"
	// DirEnv overrides where the lock files live (default: <user cache dir>/sloprail/locks).
	DirEnv = "SLOPRAIL_LOCK_DIR"
	// DefaultSlots is the machine-wide judge limit.
	DefaultSlots = 8
	// RunSlotsEnv overrides how many heavy runs (`sr-checks run`, `sr-test run`) may be in
	// progress at once on the machine.
	RunSlotsEnv = "SLOPRAIL_RUN_SLOTS"
	// RunHeldEnv is set, for the children of a run holding a run slot, so a run they start (a
	// test case running `sr-checks run`) does not queue for a second slot behind its own parent.
	RunHeldEnv = "SLOPRAIL_RUN_SLOT_HELD"

	staleAfter = 24 * time.Hour
)

// Limiter is a handle on the lock directory. The zero Poll and Notify intervals take the defaults.
type Limiter struct {
	Dir      string
	Slots    int
	RunSlots int           // heavy runs at once; 0 takes DefaultRunSlots
	RunWait  time.Duration // how long a run waits for a slot before ErrWaitExpired; 0 takes DefaultRunWait
	Out      io.Writer     // where "waiting" lines go; nil discards
	Poll     time.Duration // how often a waiter retries (default 100ms)
	Notify   time.Duration // how often a waiter says it waits (default 30s)
}

// New is the machine's limiter, configured from the environment.
func New(out io.Writer) Limiter {
	dir := os.Getenv(DirEnv)
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil || base == "" {
			base = os.TempDir()
		}
		dir = filepath.Join(base, "sloprail", "locks")
	}
	slots := DefaultSlots
	if n, err := strconv.Atoi(os.Getenv(SlotsEnv)); err == nil && n > 0 {
		slots = n
	}
	return Limiter{Dir: dir, Slots: slots, RunSlots: RunSlots(), RunWait: RunWait(), Out: out}
}

// RunWaitEnv overrides how long a run waits for a run slot.
const RunWaitEnv = "SLOPRAIL_RUN_WAIT"

// DefaultRunWait bounds the wait for a run slot: a hung holder must not hold every later run
// hostage. When it passes the run goes ahead without a slot, loudly.
const DefaultRunWait = 10 * time.Minute

// ErrWaitExpired is returned when a bounded wait for a slot ran out.
var ErrWaitExpired = errors.New("waited too long for a slot")

// RunWait is the bound on the wait for a run slot: SLOPRAIL_RUN_WAIT (a Go duration), else
// DefaultRunWait.
func RunWait() time.Duration {
	if d, err := time.ParseDuration(os.Getenv(RunWaitEnv)); err == nil && d > 0 {
		return d
	}
	return DefaultRunWait
}

// DefaultRunSlots is a quarter of the CPUs, at least two: a run is a fan-out of git, shell and jq
// processes, so a few of them already fill the machine.
func DefaultRunSlots() int { return max(2, runtime.NumCPU()/4) }

// RunSlots is the machine's heavy-run limit: SLOPRAIL_RUN_SLOTS, else DefaultRunSlots.
func RunSlots() int {
	if n, err := strconv.Atoi(os.Getenv(RunSlotsEnv)); err == nil && n > 0 {
		return n
	}
	return DefaultRunSlots()
}

// Fanout is each run's own parallelism limit under the machine's budget: the CPUs shared by the
// run slots, at least two. Runs queue for a slot, so the machine runs about NumCPU of them at once.
func Fanout() int { return max(2, runtime.NumCPU()/RunSlots()) }

func (l Limiter) poll() time.Duration {
	if l.Poll > 0 {
		return l.Poll
	}
	return 100 * time.Millisecond
}

func (l Limiter) notify() time.Duration {
	if l.Notify > 0 {
		return l.Notify
	}
	return 30 * time.Second
}

func (l Limiter) say(format string, a ...any) {
	if l.Out != nil {
		fmt.Fprintf(l.Out, format+"\n", a...)
	}
}

// tryLock takes an exclusive flock on path without blocking. ok is false when another open
// file description holds it.
func tryLock(path string) (f *os.File, ok bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, err
	}
	f, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, false, nil
		}
		return nil, false, err
	}
	return f, true, nil
}

func unlock(f *os.File) func() {
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
}

// AcquireSlot waits for one of the machine's judge slots and returns its release. Every
// Notify it says it is waiting and how many slots are busy.
func (l Limiter) AcquireSlot() (release func(), err error) {
	return l.acquireSlot("slots", "judge", l.Slots, DefaultSlots, 0)
}

// AcquireRunSlot waits for one of the machine's heavy-run slots and returns its release. A
// process whose parent already holds one (RunHeldEnv) takes none: waiting behind its own parent
// could never end.
func (l Limiter) AcquireRunSlot() (release func(), err error) {
	if os.Getenv(RunHeldEnv) != "" {
		return func() {}, nil
	}
	wait := l.RunWait
	if wait <= 0 {
		wait = DefaultRunWait
	}
	return l.acquireSlot("runslots", "run", l.RunSlots, DefaultRunSlots(), wait)
}

// HoldRunSlot is AcquireRunSlot for a process that is the run: it also marks the environment its
// children inherit with RunHeldEnv until the release.
func (l Limiter) HoldRunSlot() (release func(), err error) {
	nested := os.Getenv(RunHeldEnv) != ""
	slot, err := l.AcquireRunSlot()
	if err != nil {
		if errors.Is(err, ErrWaitExpired) && !nested {
			// Running anyway, without a slot: its children must still not queue for one.
			os.Setenv(RunHeldEnv, "1")
		}
		return nil, err
	}
	if nested {
		return slot, nil
	}
	os.Setenv(RunHeldEnv, "1")
	return func() { os.Unsetenv(RunHeldEnv); slot() }, nil
}

// Slots are not FIFO: a waiter takes the first free one it probes, so a long wait is possible
// under a steady stream of arrivals; maxWait (0: none) bounds it with ErrWaitExpired.
func (l Limiter) acquireSlot(sub, what string, n, def int, maxWait time.Duration) (release func(), err error) {
	if n < 1 {
		n = def
	}
	began := time.Now()
	last := began
	start := int(time.Now().UnixNano()) // spread the first probes so processes do not all queue on slot 0
	for {
		busy, holder := 0, ""
		for i := 0; i < n; i++ {
			idx := (start + i) % n
			f, ok, err := tryLock(filepath.Join(l.Dir, sub, "slot-"+strconv.Itoa(idx)))
			if err != nil {
				return nil, fmt.Errorf("%s slots: %w", what, err)
			}
			if ok {
				writeHolder(f)
				return unlock(f), nil
			}
			busy++
			if holder == "" {
				holder = readHolder(filepath.Join(l.Dir, sub, "slot-"+strconv.Itoa(idx)))
			}
		}
		if maxWait > 0 && time.Since(began) >= maxWait {
			return nil, fmt.Errorf("%s slots: %w (%s, %d busy%s)", what, ErrWaitExpired, maxWait.Round(time.Second), busy, holder)
		}
		if time.Since(last) >= l.notify() {
			l.say("sloprail: waiting for a %s slot (%d busy%s)", what, busy, holder)
			last = time.Now()
		}
		time.Sleep(l.poll())
	}
}

// writeHolder records who holds the slot f: pid, command and start time, for the waiters to name.
func writeHolder(f *os.File) {
	cmd := filepath.Base(os.Args[0])
	if len(os.Args) > 1 {
		cmd += " " + os.Args[1]
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(fmt.Sprintf("%d\n%s\n%d\n", os.Getpid(), cmd, time.Now().Unix())), 0)
}

// readHolder describes the holder a slot file names: ", held by pid 12 (sr-checks run, 4m12s)".
// Empty when the file says nothing.
func readHolder(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	f := strings.SplitN(string(b), "\n", 4)
	if len(f) < 3 {
		return ""
	}
	since, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil {
		return ""
	}
	return fmt.Sprintf(", held by pid %s (%s, %s)", f[0], f[1], time.Since(time.Unix(since, 0)).Round(time.Second))
}

// Name is a lock's file name for a key: a hash, so any key text is safe.
func Name(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:40]
}

// AcquireInflight takes the exclusive in-flight lock of a verdict key. When another process
// holds it, a marker is left (Contended) so the holder knows to store its verdict before it
// lets go, and the call waits for the release: waited is true then, and the caller re-checks
// the cache before judging. why is what the wait says it is for.
func (l Limiter) AcquireInflight(key, why string) (release func(), waited bool, err error) {
	return l.acquire(filepath.Join(l.Dir, "inflight", key), true, why)
}

// AcquireRun takes the lock of one run (a worktree and range); a second run of the same waits.
func (l Limiter) AcquireRun(key, why string) (release func(), err error) {
	r, _, err := l.acquire(filepath.Join(l.Dir, "runs", key), false, why)
	return r, err
}

func (l Limiter) acquire(path string, marker bool, why string) (func(), bool, error) {
	f, ok, err := tryLock(path)
	if err != nil {
		return nil, false, err
	}
	if ok {
		return unlock(f), false, nil
	}
	if marker {
		if m, err := os.Create(path + ".wait"); err == nil {
			m.Close()
		}
	}
	last := time.Now()
	for {
		time.Sleep(l.poll())
		f, ok, err := tryLock(path)
		if err != nil {
			return nil, true, err
		}
		if ok {
			return unlock(f), true, nil
		}
		if time.Since(last) >= l.notify() {
			l.say("sloprail: waiting for %s", why)
			last = time.Now()
		}
	}
}

// Contended says another process is waiting for the key's lock; ClearContended drops the marker
// once the holder has stored the verdict.
func (l Limiter) Contended(key string) bool {
	_, err := os.Stat(filepath.Join(l.Dir, "inflight", key+".wait"))
	return err == nil
}

func (l Limiter) ClearContended(key string) {
	os.Remove(filepath.Join(l.Dir, "inflight", key+".wait"))
}

// Prune removes in-flight and run lock files untouched for a day that nobody holds.
func (l Limiter) Prune() {
	for _, sub := range []string{"inflight", "runs"} {
		dir := filepath.Join(l.Dir, sub)
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			info, err := e.Info()
			if err != nil || time.Since(info.ModTime()) < staleAfter {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if f, ok, _ := tryLock(p); ok {
				os.Remove(p)
				unlock(f)()
			}
		}
	}
}
