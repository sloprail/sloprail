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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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

	staleAfter = 24 * time.Hour
)

// Limiter is a handle on the lock directory. The zero Poll and Notify intervals take the defaults.
type Limiter struct {
	Dir    string
	Slots  int
	Out    io.Writer     // where "waiting" lines go; nil discards
	Poll   time.Duration // how often a waiter retries (default 100ms)
	Notify time.Duration // how often a waiter says it waits (default 30s)
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
	return Limiter{Dir: dir, Slots: slots, Out: out}
}

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
	n := l.Slots
	if n < 1 {
		n = DefaultSlots
	}
	last := time.Now()
	start := int(time.Now().UnixNano()) // spread the first probes so processes do not all queue on slot 0
	for {
		busy := 0
		for i := 0; i < n; i++ {
			idx := (start + i) % n
			f, ok, err := tryLock(filepath.Join(l.Dir, "slots", "slot-"+strconv.Itoa(idx)))
			if err != nil {
				return nil, fmt.Errorf("judge slots: %w", err)
			}
			if ok {
				return unlock(f), nil
			}
			busy++
		}
		if time.Since(last) >= l.notify() {
			l.say("sloprail: waiting for a judge slot (%d busy)", busy)
			last = time.Now()
		}
		time.Sleep(l.poll())
	}
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
