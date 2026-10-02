package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Reading the same blob of a sha-named commit repeatedly costs one git process.
func TestBlobAtIsMemoizedPerCommit(t *testing.T) {
	dir := t.TempDir()
	g := func(args ...string) string {
		c := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	g("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	g("add", "a.md")
	g("commit", "-q", "-m", "x")
	sha := g("rev-parse", "HEAD")[:40]
	start := time.Now()
	for i := 0; i < 3000; i++ {
		got, err := BlobAt(dir, sha, "a.md")
		if err != nil || got != "hello" {
			t.Fatalf("BlobAt = %q, %v", got, err)
		}
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("3000 reads took %s: one git process each", d)
	}
}
