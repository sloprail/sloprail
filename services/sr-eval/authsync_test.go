package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A harness that saves its login by renaming a new file over the path replaces
// the link with a regular file. The refreshed login must reach the operator's
// real file, owner-only, and the sandbox path must be the link again; a link
// still in place, and a login the agent removed, are left alone.
func TestRestoreAuthLink_CarriesAReplacedLoginBack(t *testing.T) {
	realHome, home := t.TempDir(), t.TempDir()
	rel := filepath.Join(".claude", ".credentials.json")
	real, sandboxed := filepath.Join(realHome, rel), filepath.Join(home, rel)
	mustMkdirAll(t, filepath.Dir(real))
	mustWriteFile(t, real, "old-token")
	if err := linkAuthFiles(realHome, home, []string{rel}); err != nil {
		t.Fatal(err)
	}

	// Untouched link: nothing changes.
	restoreAuthLinks(realHome, home, []string{rel})
	if got := readTrimmed(t, real); got != "old-token" {
		t.Fatalf("an untouched link changed the real login to %q", got)
	}

	// The harness refreshes: a new file renamed over the link.
	tmp := sandboxed + ".tmp"
	mustWriteFile(t, tmp, "new-token")
	if err := os.Rename(tmp, sandboxed); err != nil {
		t.Fatal(err)
	}
	if got := readTrimmed(t, real); got != "old-token" {
		t.Fatalf("precondition: the rename should have left the real file stale, it holds %q", got)
	}

	stop := keepAuthLinked(context.Background(), realHome, home, []string{rel})
	stop() // the last pass alone must carry it back
	stop() // and stopping twice is safe

	if got := readTrimmed(t, real); got != "new-token" {
		t.Errorf("the refreshed login did not reach the real file: it holds %q", got)
	}
	if info, err := os.Stat(real); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the real login file must be owner-only (0600), got %v, %v", info.Mode().Perm(), err)
	}
	if info, err := os.Lstat(sandboxed); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the sandbox path is not the link again: %v, %v", info, err)
	}
	if got := readTrimmed(t, sandboxed); got != "new-token" {
		t.Errorf("the sandbox reads %q through the link, want the refreshed login", got)
	}

	// The agent logs out: the link goes, the real login stays.
	if err := os.Remove(sandboxed); err != nil {
		t.Fatal(err)
	}
	restoreAuthLinks(realHome, home, []string{rel})
	if got := readTrimmed(t, real); got != "new-token" {
		t.Errorf("a logout in the sandbox changed the real login to %q", got)
	}
}
