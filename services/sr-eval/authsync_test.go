package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/internal/harness"
)

// A harness that saves its login by renaming a new file over the path replaces
// the link with a regular file. The refreshed login must reach the operator's
// real file, owner-only, and the sandbox path must be the link again; a link
// still in place, and a login the agent removed, are left alone.
func TestRestoreAuthLink_CarriesAReplacedLoginBack(t *testing.T) {
	realHome, home := t.TempDir(), t.TempDir()
	rel := filepath.Join(".claude", ".credentials.json")
	real, sandboxed := filepath.Join(realHome, rel), filepath.Join(home, rel)
	files := []harness.AuthFile{{Real: real, InHome: rel}}
	mustMkdirAll(t, filepath.Dir(real))
	mustWriteFile(t, real, "old-token")
	if err := linkAuthFiles(home, files); err != nil {
		t.Fatal(err)
	}

	// Untouched link: nothing changes.
	restoreAuthLinks(home, files)
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

	stop := keepAuthLinked(context.Background(), home, files)
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
	restoreAuthLinks(home, files)
	if got := readTrimmed(t, real); got != "new-token" {
		t.Errorf("a logout in the sandbox changed the real login to %q", got)
	}
}

// An operator running Claude Code under CLAUDE_CONFIG_DIR is logged in THERE: the agent's
// login is linked from that file (not from ~/.claude, which may be another account's), it
// sits at the default place in the sandbox's HOME, a refresh the harness renames over it
// is carried back to that same file, and the default login is never touched.
func TestAuthFiles_FollowTheOperatorsClaudeConfigDir(t *testing.T) {
	realHome, configDir, home := t.TempDir(), t.TempDir(), t.TempDir()
	defaultLogin := filepath.Join(realHome, ".claude", ".credentials.json")
	mustMkdirAll(t, filepath.Dir(defaultLogin))
	mustWriteFile(t, defaultLogin, "other-account")
	login := filepath.Join(configDir, ".credentials.json")
	mustWriteFile(t, login, "operator-token")

	prov, err := harness.ProvisionerOf(testHarness(t, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	files := prov.AuthFiles(realHome, func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return configDir
		}
		return ""
	})
	if err := linkAuthFiles(home, files); err != nil {
		t.Fatal(err)
	}
	sandboxed := filepath.Join(home, ".claude", ".credentials.json")
	if got := readTrimmed(t, sandboxed); got != "operator-token" {
		t.Fatalf("the agent's login reads %q, want the one in CLAUDE_CONFIG_DIR", got)
	}

	tmp := sandboxed + ".tmp"
	mustWriteFile(t, tmp, "refreshed")
	if err := os.Rename(tmp, sandboxed); err != nil {
		t.Fatal(err)
	}
	keepAuthLinked(context.Background(), home, files)()
	if got := readTrimmed(t, login); got != "refreshed" {
		t.Errorf("the refresh was not carried back to CLAUDE_CONFIG_DIR's login: %q", got)
	}
	if got := readTrimmed(t, defaultLogin); got != "other-account" {
		t.Errorf("the default ~/.claude login was written: %q", got)
	}
}
