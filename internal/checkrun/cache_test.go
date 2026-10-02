package checkrun

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/checkcache"
)

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// A result whose push failed is warned about in one line, and the next write OpenCache (what run
// starts with) pushes it, so the warning goes away once the remote is reachable.
func TestPendingPushIsWarnedAndPushedByTheNextOpen(t *testing.T) {
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	remote := filepath.Join(t.TempDir(), "remote.git") // not there yet: offline
	gitT(t, repo, "remote", "add", "origin", remote)

	var w bytes.Buffer
	store, err := OpenCache(&w, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	run := checkcache.Run{ID: "run_1", RunAt: "2026-01-01T00:00:00Z", Rule: "r", RuleHash: "h", BaseRef: "b", HeadRef: "h",
		Checks: []checkcache.Check{{Subject: "s", Kind: "script", Fingerprint: "f", Status: "pass"}}}
	if err := store.Put([]checkcache.Run{run}); err != nil {
		t.Fatal(err)
	}
	w.Reset()
	WarnPending(&w, store)
	got := w.String()
	if !strings.HasPrefix(got, "sloprail: results stored locally, not pushed: ") ||
		!strings.Contains(got, "; CI verify will say not judged until they are\n") || strings.Count(got, "\n") != 1 {
		t.Fatalf("want a one-line warning, got %q", got)
	}

	gitT(t, filepath.Dir(remote), "init", "-q", "--bare", remote) // back online
	w.Reset()
	again, err := OpenCache(&w, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.PendingPush(); err != nil {
		t.Fatalf("opening the cache must push what was pending: %v", err)
	}
	w.Reset()
	WarnPending(&w, again)
	if w.Len() != 0 {
		t.Fatalf("nothing pending, nothing said: %q", w.String())
	}
	gitT(t, remote, "rev-parse", "--verify", "sloprail/checks")
}

// verify and show open the cache read-only: they never push what is pending, and leave the
// local ref and the remote exactly as they were.
func TestReadOnlyOpenNeverPushesOrWrites(t *testing.T) {
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	remote := filepath.Join(t.TempDir(), "remote.git")
	gitT(t, repo, "remote", "add", "origin", remote)

	var w bytes.Buffer
	store, err := OpenCache(&w, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	run := checkcache.Run{ID: "run_1", RunAt: "2026-01-01T00:00:00Z", Rule: "r", RuleHash: "h", BaseRef: "b", HeadRef: "h",
		Checks: []checkcache.Check{{Subject: "s", Kind: "script", Fingerprint: "f", Status: "pass"}}}
	if err := store.Put([]checkcache.Run{run}); err != nil {
		t.Fatal(err)
	}
	ref := func() string { return gitOut(t, repo, "rev-parse", "--verify", "-q", "refs/sloprail/checks") }
	before := ref()

	gitT(t, filepath.Dir(remote), "init", "-q", "--bare", remote) // back online
	ro, err := OpenCache(&w, repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if ref() != before {
		t.Fatal("a read-only open moved the local results ref")
	}
	if ro.PendingPush() != nil {
		t.Fatal("a read-only open tried to push")
	}
	if out := gitOut(t, remote, "for-each-ref", "refs/heads"); strings.TrimSpace(out) != "" {
		t.Fatalf("a read-only open pushed to the remote: %s", out)
	}
	if _, err := OpenCache(&w, repo, true); err != nil {
		t.Fatal(err)
	}
	if out := gitOut(t, remote, "for-each-ref", "refs/heads"); !strings.Contains(out, "sloprail/checks") {
		t.Fatalf("a write open must push: %q", out)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, _ := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out))
}
