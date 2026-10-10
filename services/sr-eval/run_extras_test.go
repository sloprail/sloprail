package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The judges' transcripts are found beside the agent's project directory and filed by rule;
// another project's judges are not taken.
func TestJudgeFiles(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "-tmp-p")
	held := proj + "--sloprail-file-guard-invariant-held"
	other := filepath.Join(root, "-tmp-q--sloprail-file-guard-x") // another project's judge
	for _, d := range []string{proj, held, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cost := func(string) string { return `{"type":"user"}` + "\n" }
	agent := filepath.Join(proj, "a.jsonl")
	mustWriteFile(t, agent, cost("0.5"))
	mustWriteFile(t, filepath.Join(held, "j1.jsonl"), cost("0.25"))
	mustWriteFile(t, filepath.Join(held, "j2.jsonl"), cost("0.25"))
	mustWriteFile(t, filepath.Join(other, "z.jsonl"), cost("9"))

	judges := judgeFiles(agent)
	if len(judges) != 2 || judges["judges/file-guard-invariant-held/j1.jsonl"] == nil {
		t.Fatalf("judges: %v", keysOf(judges))
	}
}

// The bundle restores the repository with every branch, the check results' one included.
func TestRepoBundle_HoldsEveryBranch(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	mustWriteFile(t, filepath.Join(repo, "a"), "a")
	git("add", "-A")
	git("commit", "-q", "-m", "one")
	git("branch", "sloprail/checks")
	body, err := repoBundle(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(t.TempDir(), "repo.bundle")
	if err := os.WriteFile(b, body, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "bundle", "list-heads", b).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, want := range []string{"refs/heads/main", "refs/heads/sloprail/checks"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the bundle lacks %s:\n%s", want, out)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	var k []string
	for name := range m {
		k = append(k, name)
	}
	return k
}
