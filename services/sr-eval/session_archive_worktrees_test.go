package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/gitrepo"
)

// Worktrees of one repository share its check verdicts, so they are archived once.
func TestArchive_ChecksOncePerRepositoryAcrossWorktrees(t *testing.T) {
	w := newArchiveWorld(t, "s1")
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
    printf '[{"Folder":"%s","Base":"%s","HeadSHA":"%s"},{"Folder":"%s","Base":"%s","HeadSHA":"%s"},{"Folder":"%s","Base":"%s","HeadSHA":"%s"},{"Folder":"/no/such/folder"}]';;
  *) exit 3;;
esac
`, w.other, gitrepo.EmptyTree, w.otherSHA, wt1, gitrepo.EmptyTree, w.otherSHA, wt2, gitrepo.EmptyTree, w.otherSHA))
	mustWriteFile2(t, filepath.Join(bin, "sr-checks"), `#!/bin/sh
case "$*" in
  "log --json"*) printf '{"rule":"r","from":"%s","args":"%s"}\n' "$(pwd -P)" "$*";;
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
	// three worktrees held the same range: it is asked for once
	got, _ := os.ReadFile(filepath.Join(dir, c.File))
	if want := `"args":"log --json --range ` + gitrepo.EmptyTree + ".." + w.otherSHA + `"`; !strings.Contains(string(got), want) {
		t.Errorf("want one --range for the repository (%s): %s", want, got)
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

// rangeOf is the range of the whole history of repo, as a session tracking it from the start holds it.
func rangeOf(t *testing.T, repo string) []trackedRange {
	t.Helper()
	return []trackedRange{{Folder: repo, Base: gitrepo.EmptyTree, HeadSHA: gitOut(t, repo, "rev-parse", "HEAD")}}
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
	archiveChecks(m, map[string][]string{}, nil, t.TempDir())
	if len(m.Checks) != 0 || len(m.Skipped) != 0 {
		t.Fatalf("checks %+v skipped %+v", m.Checks, m.Skipped)
	}
}

// A repository whose checks store cannot be read is skipped, not archived as an empty file,
// and does not stop the other repository from being archived.
func TestArchiveChecks_RepositoryWithoutChecksStoreIsSkipped(t *testing.T) {
	good, bad := gitInit(t, t.TempDir()), gitInit(t, t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	commitEmpty(t, good)
	commitEmpty(t, bad)
	fakeChecks(t, `if [ "$(pwd -P)" = "`+bad+`" ]; then echo "no checks store" >&2; exit 2; fi; echo '{"rule":"r"}'`)
	dir := t.TempDir()
	m := &archiveManifest{}
	archiveChecks(m, map[string][]string{good: {"s1"}, bad: {"s1"}}, map[string][]trackedRange{good: rangeOf(t, good), bad: rangeOf(t, bad)}, dir)
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

// The main worktree is gone but a linked one stands: git cannot read the linked worktree either (its
// git directory lived in the main one), so no range of it resolves. The repository still gets its file,
// empty, and the range is reported, never widened to an unscoped log.
func TestArchiveChecks_MainWorktreeGoneLinkedStands(t *testing.T) {
	main := gitInit(t, t.TempDir())
	gitOut(t, main, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "--quiet", "--allow-empty", "-m", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, main, "worktree", "add", "--quiet", "-b", "b", wt)
	wt, _ = filepath.EvalSymlinks(wt)
	fakeChecks(t, "echo unscoped")
	sha := gitOut(t, main, "rev-parse", "HEAD")
	if err := os.RemoveAll(main); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	m := &archiveManifest{}
	archiveChecks(m, map[string][]string{wt: {"s1"}}, map[string][]trackedRange{wt: {{Folder: wt, Base: gitrepo.EmptyTree, HeadSHA: sha}}}, dir)
	if len(m.Checks) != 1 || len(m.Skipped) != 1 || !strings.Contains(m.Skipped[0].Reason, "not in the repository") {
		t.Fatalf("checks %+v skipped %+v", m.Checks, m.Skipped)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, m.Checks[0].File)); len(got) != 0 {
		t.Fatalf("want an empty file, not an unscoped log: %s", got)
	}
}

// A symlinked spelling of a tracked folder and its real path are one repository.
func TestArchiveChecks_SameRepositoryTwoSpellings(t *testing.T) {
	repo := gitInit(t, t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	commitEmpty(t, repo)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	fakeChecks(t, `echo '{"rule":"r"}'`)
	m := &archiveManifest{}
	archiveChecks(m, map[string][]string{repo: {"s1"}, link: {"s2"}}, map[string][]trackedRange{repo: rangeOf(t, repo)}, t.TempDir())
	if len(m.Checks) != 1 {
		t.Fatalf("checks: %+v", m.Checks)
	}
	c := m.Checks[0]
	if len(c.Folders) != 2 || strings.Join(c.Sessions, ",") != "s1,s2" {
		t.Fatalf("%+v", c)
	}
}

// The log of a repository is cut to the ranges the session tracked there: the base and every head a
// range pointed at (first tip, last tip, the branch now) make one --range each, and a range whose
// commits are not in the repository is reported, never silently widened to the whole log.
func TestRangeArgs_OnePerDistinctHeadFromTheBase(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	repo := gitInit(t, t.TempDir())
	base := commitEmpty(t, repo)
	first := commitEmpty(t, repo)
	gitOut(t, repo, "branch", "-M", "feat")
	last := commitEmpty(t, repo)
	got, unresolved := rangeArgs(repo, []trackedRange{
		{Folder: repo, Base: base, Head: "feat", HeadSHA: first, FirstTip: first},
		{Folder: repo, Base: base, Head: "feat", HeadSHA: last, FirstTip: first}, // same range again, moved on
		{Folder: repo, Base: base, Head: "detached/abc", HeadSHA: last},
	})
	want := []string{base + ".." + first, base + ".." + last}
	if want[0] > want[1] {
		want[0], want[1] = want[1], want[0]
	}
	if strings.Join(got, ",") != strings.Join(want, ",") || len(unresolved) != 0 {
		t.Fatalf("args %v unresolved %v, want %v", got, unresolved, want)
	}
	_, unresolved = rangeArgs(repo, []trackedRange{
		{Folder: repo, Base: "no-such-base", HeadSHA: last},
		{Folder: repo, Base: base, Head: "gone-branch"},
	})
	if len(unresolved) != 2 {
		t.Fatalf("want both ranges reported unresolved, got %v", unresolved)
	}
}

// A repository the session tracked no range in gets an empty file, and sr-checks is not asked.
func TestArchiveChecks_NoRangeMeansAnEmptyFile(t *testing.T) {
	repo := gitInit(t, t.TempDir())
	fakeChecks(t, "echo unscoped; exit 0")
	dir := t.TempDir()
	m := &archiveManifest{}
	archiveChecks(m, map[string][]string{repo: nil}, nil, dir)
	if len(m.Checks) != 1 || len(m.Skipped) != 0 {
		t.Fatalf("checks %+v skipped %+v", m.Checks, m.Skipped)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, m.Checks[0].File)); len(got) != 0 {
		t.Fatalf("want an empty file, got %q", got)
	}
}
