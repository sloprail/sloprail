// Package reap removes the temp directories sloprail's runs leave behind when they are killed
// (sr-test-*, sr-tree-*, sr-agent-output*): a run that dies skips its deferred cleanup, and
// thousands of them pile up in the OS temp dir. It runs when a heavy run starts.
//
// A directory is garbage when its owner is gone. Directories made by this build carry an owner
// file (Mark): its pid is dead. Older ones carry none, so only age says: untouched for a day.
// Nothing a live process owns is ever touched, and a failure to remove one is ignored: reaping is
// housekeeping, never part of the run's verdict.
package reap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sloprail/sloprail/internal/gitrepo"
)

// OwnerFile names the marker Mark writes in a temp directory: the pid that made it.
const OwnerFile = "sr-owner"

// LegacyAge is how old a directory with no owner file must be before it is taken.
const LegacyAge = 24 * time.Hour

// Patterns are the directory name prefixes this package takes.
var Patterns = []string{"sr-test-", "sr-agent-output"}

// Mark records the current process as the owner of dir, so Temp can tell a dead owner's from a
// live run's. Best effort: an unmarked directory falls back to the age rule.
func Mark(dir string) {
	_ = os.WriteFile(filepath.Join(dir, OwnerFile), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
}

// ownerGone reports whether dir's marked owner is dead; false when unmarked or unreadable.
func ownerGone(dir string) (gone, marked bool) {
	b, err := os.ReadFile(filepath.Join(dir, OwnerFile))
	if err != nil {
		return false, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false, true
	}
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH), true
}

// Temp removes the stale sr-test-*, sr-agent-output* and sr-tree-* directories directly under
// tmp ("" = the OS temp dir) and returns how many it removed. A stale sr-tree-* may still be a
// registered worktree: repo, when not empty, is where gitrepo's sweep unregisters those first.
func Temp(tmp, repo string) int {
	if tmp == "" {
		tmp = os.TempDir()
	}
	if repo != "" {
		gitrepo.SweepStaleSnapshots(repo)
	}
	ents, err := os.ReadDir(tmp)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		name, path := e.Name(), filepath.Join(tmp, e.Name())
		switch {
		case strings.HasPrefix(name, "sr-tree-"):
			if !gitrepo.SnapshotOrphaned(path) {
				continue
			}
		case hasPrefix(name):
			if gone, marked := ownerGone(path); marked {
				if !gone {
					continue
				}
			} else if info, err := e.Info(); err != nil || time.Since(info.ModTime()) < LegacyAge {
				continue
			}
		default:
			continue
		}
		if removeAll(path) == nil {
			n++
		}
	}
	return n
}

func hasPrefix(name string) bool {
	for _, p := range Patterns {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// removeAll is os.RemoveAll that first makes a read-only snapshot checkout writable.
func removeAll(path string) error {
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("reap %s: %w", path, err)
	}
	return nil
}
