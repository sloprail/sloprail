package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Worktrees of one repository share its check verdicts, so they are archived once.
func TestArchive_ChecksOncePerRepositoryAcrossWorktrees(t *testing.T) {
	w := newArchiveWorld(t, "s1")
	if out, err := exec.Command("git", "-C", w.other, "-c", "user.name=t", "-c", "user.email=t@t",
		"commit", "--quiet", "--allow-empty", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v: %s", err, out)
	}
	wt1 := filepath.Join(t.TempDir(), "wt1")
	wt2 := filepath.Join(t.TempDir(), "wt2")
	for i, wt := range []string{wt1, wt2} {
		gitOut(t, w.other, "worktree", "add", "--quiet", "-b", fmt.Sprintf("b%d", i), wt)
	}
	wt1, _ = filepath.EvalSymlinks(wt1)
	wt2, _ = filepath.EvalSymlinks(wt2)

	bin := t.TempDir()
	mustWriteFile2(t, filepath.Join(bin, "sr-session"), fmt.Sprintf(`#!/bin/sh
case "$*" in
  "refs list --json") cat >/dev/null
    printf '[{"Folder":"%s"},{"Folder":"%s"},{"Folder":"%s"},{"Folder":"/no/such/folder"}]';;
  *) exit 3;;
esac
`, w.other, wt1, wt2))
	mustWriteFile2(t, filepath.Join(bin, "sr-checks"), `#!/bin/sh
case "$*" in
  "log --json") printf '{"rule":"r","from":"%s"}\n' "$(pwd -P)";;
  *) exit 3;;
esac
`)
	for _, n := range []string{"sr-session", "sr-checks"} {
		if err := os.Chmod(filepath.Join(bin, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	dir, err := runArchiveCLI(t, "--session", "s1", "--into", w.into)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	var m archiveManifest
	body, _ := os.ReadFile(filepath.Join(dir, "archive.json"))
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	byRepo := map[string]archivedChecks{}
	for _, c := range m.Checks {
		byRepo[c.Repo] = c
	}
	if len(m.Checks) != 2 || len(byRepo) != 2 {
		t.Fatalf("want one entry per repository (project, other): %+v", m.Checks)
	}
	c, ok := byRepo[w.other]
	if !ok {
		t.Fatalf("no entry for %s: %+v", w.other, m.Checks)
	}
	if got, want := strings.Join(c.Folders, ","), strings.Join(sortedStrings(w.other, wt1, wt2), ","); got != want {
		t.Errorf("folders of the repository: %s, want %s", got, want)
	}
	if len(c.Sessions) != 1 || c.Sessions[0] != "s1" {
		t.Errorf("sessions: %v", c.Sessions)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "checks", "*.jsonl")); len(files) != 2 {
		t.Errorf("want two checks files, got %v", files)
	}
	var gone bool
	for _, s := range m.Skipped {
		gone = gone || (strings.Contains(s.Item, "/no/such/folder") && s.Reason == "the folder is gone")
	}
	if !gone {
		t.Errorf("vanished folder not recorded: %+v", m.Skipped)
	}
}

func sortedStrings(s ...string) []string {
	out := append([]string(nil), s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
