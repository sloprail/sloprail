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

func fakeChecks(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	mustWriteFile2(t, filepath.Join(bin, "sr-checks"), "#!/bin/sh\n"+script+"\n")
	if err := os.Chmod(filepath.Join(bin, "sr-checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// No tracked folder at all: nothing is archived and nothing is skipped.
func TestArchiveChecks_ZeroRepositories(t *testing.T) {
	fakeChecks(t, "exit 9")
	m := &archiveManifest{}
	archiveChecks(m, map[string][]string{}, t.TempDir())
	if len(m.Checks) != 0 || len(m.Skipped) != 0 {
		t.Fatalf("checks %+v skipped %+v", m.Checks, m.Skipped)
	}
}

// A repository whose checks store cannot be read is skipped, not archived as an empty file,
// and does not stop the other repository from being archived.
func TestArchiveChecks_RepositoryWithoutChecksStoreIsSkipped(t *testing.T) {
	good, bad := gitInit(t, t.TempDir()), gitInit(t, t.TempDir())
	fakeChecks(t, `if [ "$(pwd -P)" = "`+bad+`" ]; then echo "no checks store" >&2; exit 2; fi; echo '{"rule":"r"}'`)
	dir := t.TempDir()
	m := &archiveManifest{}
	archiveChecks(m, map[string][]string{good: {"s1"}, bad: {"s1"}}, dir)
	if len(m.Checks) != 1 || m.Checks[0].Repo != good {
		t.Fatalf("checks: %+v", m.Checks)
	}
	if len(m.Skipped) != 1 || m.Skipped[0].Item != "checks of "+bad || !strings.Contains(m.Skipped[0].Reason, "no checks store") {
		t.Fatalf("skipped: %+v", m.Skipped)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "checks", "*.jsonl")); len(files) != 1 {
		t.Fatalf("files: %v", files)
	}
}

// The main worktree is gone but a linked one stands: the log is still read, from the linked folder.
func TestArchiveChecks_MainWorktreeGoneLinkedStands(t *testing.T) {
	main := gitInit(t, t.TempDir())
	gitOut(t, main, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "--quiet", "--allow-empty", "-m", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, main, "worktree", "add", "--quiet", "-b", "b", wt)
	wt, _ = filepath.EvalSymlinks(wt)
	fakeChecks(t, `echo "{\"from\":\"$(pwd -P)\"}"`)
	if err := os.RemoveAll(main); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	m := &archiveManifest{}
	archiveChecks(m, map[string][]string{wt: {"s1"}}, dir)
	if len(m.Checks) != 1 || len(m.Skipped) != 0 {
		t.Fatalf("checks %+v skipped %+v", m.Checks, m.Skipped)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, m.Checks[0].File)); !strings.Contains(string(got), wt) {
		t.Fatalf("log was not read from the standing worktree: %s", got)
	}
}

// A symlinked spelling of a tracked folder and its real path are one repository.
func TestArchiveChecks_SameRepositoryTwoSpellings(t *testing.T) {
	repo := gitInit(t, t.TempDir())
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	fakeChecks(t, `echo '{"rule":"r"}'`)
	m := &archiveManifest{}
	archiveChecks(m, map[string][]string{repo: {"s1"}, link: {"s2"}}, t.TempDir())
	if len(m.Checks) != 1 {
		t.Fatalf("checks: %+v", m.Checks)
	}
	c := m.Checks[0]
	if len(c.Folders) != 2 || strings.Join(c.Sessions, ",") != "s1,s2" {
		t.Fatalf("%+v", c)
	}
}
