package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchive_TempDirOfAnotherSessionIsNeverPicked(t *testing.T) {
	w := newArchiveWorld(t, "mine")
	other := filepath.Join(t.TempDir(), "claude-777", "-some-project", "theirs")
	mustWriteFile2(t, filepath.Join(other, "scratchpad", "x.txt"), "theirs")
	mustWriteFile2(t, filepath.Join(w.projDir, "mine.jsonl"), `{"text":"`+other+`/scratchpad"}`+"\n")
	dir, err := runArchiveCLI(t, "--session", "mine", "--into", w.into)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine", "tmp", "scratchpad", "x.txt")); err == nil {
		t.Error("another session's temp dir was archived as this session's")
	}
	if _, err := os.Stat(filepath.Join(dir, "mine", "tmp", "scratchpad", "notes.txt")); err != nil {
		t.Errorf("its own temp dir was not archived: %v", err)
	}
}

func TestCopyTreeLenient_SkipsNestedGitSymlinkedDirsAndHugeFiles(t *testing.T) {
	src := t.TempDir()
	mustWriteFile2(t, filepath.Join(src, "ok.txt"), "ok")
	mustWriteFile2(t, filepath.Join(src, "repo", ".git", "HEAD"), "ref")
	mustWriteFile2(t, filepath.Join(src, "repo", "f.txt"), "f")
	if err := os.Symlink(filepath.Join(src, "repo"), filepath.Join(src, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(src, "nowhere"), filepath.Join(src, "dangling")); err != nil {
		t.Fatal(err)
	}
	huge, err := os.Create(filepath.Join(src, "huge.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := huge.Truncate(maxLenientFileSize + 1); err != nil {
		t.Fatal(err)
	}
	huge.Close()
	dst := filepath.Join(t.TempDir(), "out")
	skipped, err := copyTreeLenient(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ok.txt", filepath.Join("repo", "f.txt")} {
		if _, err := os.Stat(filepath.Join(dst, want)); err != nil {
			t.Errorf("%s not copied: %v", want, err)
		}
	}
	for _, not := range []string{filepath.Join("repo", ".git"), "linkdir", "dangling", "huge.bin"} {
		if _, err := os.Lstat(filepath.Join(dst, not)); err == nil {
			t.Errorf("%s should not have been copied", not)
		}
	}
	if len(skipped) != 4 {
		t.Errorf("want 4 skipped entries reported, got %v", skipped)
	}
}

func TestArchive_CommitsOnlyItsOwnDirectory(t *testing.T) {
	w := newArchiveWorld(t, "s")
	if err := ensureArchiveRepo(w.into); err != nil {
		t.Fatal(err)
	}
	mustWriteFile2(t, filepath.Join(w.into, "stray.txt"), "staged by hand")
	if out, err := exec.Command("git", "-C", w.into, "add", "stray.txt").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := runArchiveCLI(t, "--session", "s", "--into", w.into, "--label", "l"); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", w.into, "show", "--name-only", "--format=", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "stray.txt") || !strings.Contains(string(out), "l/") {
		t.Errorf("the commit must hold only the archive's own dir:\n%s", out)
	}
}
