package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// replaceInRef commits, on top of the results ref, a tree in which the file under dir with the
// given suffix holds the given bytes: a store a build cannot read.
func (f *fixture) replaceInRef(t *testing.T, dir, suffix, content string) {
	t.Helper()
	listing := f.e.Git(f.proj, "ls-tree", "-r", "--name-only", "refs/sloprail/checks", "--", dir+"/")
	var path string
	for _, p := range strings.Split(listing, "\n") {
		if strings.HasSuffix(p, suffix) {
			path = p
		}
	}
	if path == "" {
		t.Fatalf("no %s file under %s in:\n%s", suffix, dir, listing)
	}
	f.putInRef(t, path, content)
}

// putInRef commits, on top of the results ref, a tree in which path holds content.
func (f *fixture) putInRef(t *testing.T, path, content string) {
	t.Helper()
	idx := t.TempDir() + "/index"
	g := func(stdin string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = f.proj
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+idx, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	var parents []string // none when the ref does not exist yet
	probe := exec.Command("git", "rev-parse", "--verify", "-q", "refs/sloprail/checks")
	probe.Dir = f.proj
	if out, err := probe.Output(); err == nil {
		tip := strings.TrimSpace(string(out))
		g("", "read-tree", tip)
		parents = []string{"-p", tip}
	}
	blob := g(content, "hash-object", "-w", "--stdin")
	g("", "update-index", "--add", "--cacheinfo", "100644,"+blob+","+path)
	commit := g("", append(append([]string{"commit-tree", g("", "write-tree")}, parents...), "-m", "break the store")...)
	g("", "update-ref", "refs/sloprail/checks", commit)
}

// T002_06: a results store that is corrupt, or written by a newer schema, is an error: `verify`
// and `run` say so and fail, and neither reads it as "not judged yet" nor asks the judge.
// sr:proves cache/store-failures-not-misses
func TestT002_06_ACorruptOrNewerStoreIsAnErrorNotAMiss(t *testing.T) {
	t.Run("corrupt segment", func(t *testing.T) {
		f := newFixture(t)
		f.e.InstallJudgeClaudeCapturing(f.proj, promptFile, `{"pass": true, "reasoning": "fine"}`)
		if r := f.e.CheckRunRaw(f.proj, "s-first", f.base, f.head); r.Code != 0 {
			t.Fatalf("premise: the first run: exit %d:\n%s", r.Code, r.Output)
		}
		if n := f.judgeCalls(); n != 1 {
			t.Fatalf("premise: judged %d times, want 1", n)
		}
		f.replaceInRef(t, currentDir, ".zst", "this is not a segment\n")

		v := f.verify(f.base, f.head)
		if v.Code == 0 || strings.Contains(v.Output, "not judged yet") || !strings.Contains(strings.ToLower(v.Output), "corrupt") {
			t.Fatalf("verify over a corrupt store: exit %d, want an error naming the corruption, not a miss:\n%s", v.Code, v.Output)
		}
		r := f.e.CheckRunRaw(f.proj, "s-second", f.base, f.head)
		if r.Code == 0 || !strings.Contains(strings.ToLower(r.Output), "corrupt") {
			t.Fatalf("run over a corrupt store: exit %d, want an error naming the corruption:\n%s", r.Code, r.Output)
		}
		if n := f.judgeCalls(); n != 1 {
			t.Fatalf("the corrupt store was read as a miss: the judge was asked %d times in all, want 1", n)
		}
	})

	t.Run("newer schema", func(t *testing.T) {
		f := newFixture(t)
		f.e.InstallJudgeClaudeCapturing(f.proj, promptFile, `{"pass": true, "reasoning": "fine"}`)
		f.putInRef(t, "v2099-01-01/MANIFEST.json", "{}\n") // a directory a future schema wrote

		v := f.verify(f.base, f.head)
		if v.Code == 0 || strings.Contains(v.Output, "not judged yet") || !strings.Contains(v.Output, "newer schema") {
			t.Fatalf("verify over a newer-schema store: exit %d, want an error naming the schema, not a miss:\n%s", v.Code, v.Output)
		}
		r := f.e.CheckRunRaw(f.proj, "s-newer", f.base, f.head)
		if r.Code == 0 || !strings.Contains(r.Output, "newer schema") {
			t.Fatalf("run over a newer-schema store: exit %d, want an error naming the schema:\n%s", r.Code, r.Output)
		}
		if n := f.judgeCalls(); n != 0 {
			t.Fatalf("the unreadable store was read as a miss: the judge was asked %d times, want 0", n)
		}
	})
}
