package gitrepo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ownerFile marks a snapshot root and names the process that made it (pid and start time, so
// a recycled pid is not mistaken for the owner). It is the one marker of "this is a sloprail
// snapshot", for the sweep and for session tracking alike.
const ownerFile = "sr-snapshot-owner"

// ownerlessGrace is how old a snapshot with no owner file (made by a build before owner files
// existed) must be before the sweep takes it.
const ownerlessGrace = time.Hour

// IsSnapshot reports whether path is a sloprail snapshot checkout or lies inside one.
func IsSnapshot(path string) bool {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		if filepath.Base(p) == "tree" {
			if _, err := os.Stat(filepath.Join(filepath.Dir(p), ownerFile)); err == nil {
				return true
			}
		}
		if filepath.Dir(p) == p {
			return false
		}
	}
}

var processStart = func(pid int) string {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func writeOwner(root string) error {
	pid := os.Getpid()
	return os.WriteFile(filepath.Join(root, ownerFile), []byte(fmt.Sprintf("%d\n%s\n", pid, processStart(pid))), 0o644)
}

// ownerAlive reports whether the owner of root may still run. It answers false (dead) only when
// the pid is gone, or when both start times are known and differ; anything unknown or failing
// keeps the snapshot, since deleting a live run's checkout is the worse mistake.
func ownerAlive(root string) bool {
	b, err := os.ReadFile(filepath.Join(root, ownerFile))
	if err != nil {
		return true
	}
	lines := strings.SplitN(string(b), "\n", 3)
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || pid <= 0 {
		return true
	}
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return false
	}
	recorded := ""
	if len(lines) > 1 {
		recorded = strings.TrimSpace(lines[1])
	}
	now := processStart(pid)
	if recorded == "" || now == "" {
		return true
	}
	return recorded == now
}

// SweepStaleSnapshots removes the snapshots of this repository whose owner is dead: made
// writable, unregistered, deleted, pruned. A live owner's snapshot is never touched. The sweep is
// housekeeping: when the worktree lock is busy it is skipped (the next snapshot sweeps), so it
// never adds a wait of its own in front of the caller's real work.
func SweepStaleSnapshots(dir string) {
	_ = lockWorktrees(dir, 0, func() error { sweepStale(dir); return nil })
}

func sweepStale(dir string) {
	out, err := run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		path, ok := strings.CutPrefix(line, "worktree ")
		if !ok || filepath.Base(path) != "tree" {
			continue
		}
		root := filepath.Dir(path)
		if !strings.HasPrefix(filepath.Base(root), "sr-tree-") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, ownerFile)); err == nil {
			if ownerAlive(root) {
				continue
			}
		} else if info, serr := os.Stat(root); serr == nil && time.Since(info.ModTime()) < ownerlessGrace {
			continue
		}
		_ = setWritable(root, true)
		_, _ = run(dir, "worktree", "remove", "--force", path)
		_ = os.RemoveAll(root)
	}
	_, _ = run(dir, "worktree", "prune")
}

var live = struct {
	sync.Mutex
	set map[*Snapshot]bool
}{set: map[*Snapshot]bool{}}

func track(s *Snapshot)   { live.Lock(); live.set[s] = true; live.Unlock() }
func untrack(s *Snapshot) { live.Lock(); delete(live.set, s); live.Unlock() }

// RemoveLiveSnapshots removes every snapshot this process still holds.
func RemoveLiveSnapshots() {
	live.Lock()
	var all []*Snapshot
	for s := range live.set {
		all = append(all, s)
	}
	live.Unlock()
	for _, s := range all {
		_ = s.Remove()
	}
}

// CleanupOnSignal makes SIGTERM and SIGINT remove this process's live snapshots and then exit
// with the conventional 128+signal status. The returned func stops the handler.
func CleanupOnSignal() (stop func()) {
	ch := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case sig := <-ch:
			RemoveLiveSnapshots()
			code := 128 + int(syscall.SIGTERM)
			if sig == syscall.SIGINT {
				code = 128 + int(syscall.SIGINT)
			}
			os.Exit(code)
		case <-done:
		}
	}()
	return func() { signal.Stop(ch); close(done) }
}

// MarkOwner records the current process (pid and start time) as the owner of dir, the way a
// snapshot root is marked, so a later run can tell a dead owner's directory from a live one's.
func MarkOwner(dir string) { _ = writeOwner(dir) }

// OwnerGone reports whether dir's marked owner is dead; marked is false when dir carries no
// owner file. Anything unknown counts as alive (the pid-reuse-safe check of ownerAlive).
func OwnerGone(dir string) (gone, marked bool) {
	if _, err := os.Stat(filepath.Join(dir, ownerFile)); err != nil {
		return false, false
	}
	return !ownerAlive(dir), true
}

// SnapshotOrphaned reports whether root, a temp directory made for a snapshot (sr-tree-*), was
// left behind: its owner is dead, or it has no owner file and is older than ownerlessGrace. A
// registered worktree inside is the sweep's to unregister (SweepStaleSnapshots); this decides
// only whether the directory is garbage.
func SnapshotOrphaned(root string) bool {
	if _, err := os.Stat(filepath.Join(root, ownerFile)); err == nil {
		return !ownerAlive(root)
	}
	info, err := os.Stat(root)
	return err == nil && time.Since(info.ModTime()) >= ownerlessGrace
}
