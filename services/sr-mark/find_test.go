package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, a := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", a, err, out)
		}
	}
	return dir
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir string) {
	t.Helper()
	for _, a := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", a, err, out)
		}
	}
}

func runFindCmd(dir string, args ...string) (string, int) {
	cmd := newRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(append(append([]string{"find"}, args...), "--root", dir))
	err := cmd.Execute()
	if err == nil {
		return out.String(), 0
	}
	return out.String(), exitCode(err)
}

// sr:proves cli/mark-find-reads-committed-markers-only
func TestFind_CommittedMarkersWithEveryLeader(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, ".github/workflows/a.yml", "jobs:\n  # sr:ci verify\n  x: 1\n")
	write(t, dir, "Jenkinsfile", "// sr:ci verify\nsh 'x'\n")
	write(t, dir, "q.sql", "-- sr:ci verify\n")
	commit(t, dir)
	out, code := runFindCmd(dir, "ci", "--fqn", "verify")
	if code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, out)
	}
	for _, want := range []string{".github/workflows/a.yml:2 ci verify", "Jenkinsfile:1 ci verify", "q.sql:1 ci verify"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// sr:proves cli/mark-find-reads-committed-markers-only
func TestFind_UncommittedIsNotFound(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, "a.yml", "x: 1\n")
	commit(t, dir)
	write(t, dir, "ci.yml", "# sr:ci verify\n")
	if out, code := runFindCmd(dir, "ci"); code != 1 {
		t.Fatalf("an uncommitted marker: exit %d, want 1: %s", code, out)
	}
}

func TestFind_KindAndFQNFilter(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, "a.yml", "# sr:ci other\n# sr:docs verify\n")
	commit(t, dir)
	if _, code := runFindCmd(dir, "ci", "--fqn", "verify"); code != 1 {
		t.Fatalf("wrong fqn/kind matched: exit %d", code)
	}
	if out, code := runFindCmd(dir, "ci"); code != 0 || !strings.Contains(out, "ci other") || strings.Contains(out, "docs") {
		t.Fatalf("kind filter: exit %d %q", code, out)
	}
}

func TestFind_TextThatIsNotAMarkerIsNotOne(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, "a.yml", "# sr-mark: ci-verify\nrun: echo # sr:ci verify\n")
	commit(t, dir)
	if out, code := runFindCmd(dir, "ci", "--fqn", "verify"); code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, out)
	}
}

// sr:proves cli/mark-find-reads-committed-markers-only
func TestFind_ErrorsAreExitTwo(t *testing.T) {
	if _, code := runFindCmd(t.TempDir(), "ci"); code != 2 {
		t.Fatalf("not a repository: exit %d, want 2", code)
	}
	dir := gitRepo(t)
	write(t, dir, "a.yml", "x\n")
	commit(t, dir)
	if _, code := runFindCmd(dir, "ci", "--rev", "nope"); code != 2 {
		t.Fatalf("unknown rev: exit %d, want 2", code)
	}
}
