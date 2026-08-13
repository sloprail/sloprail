package fingerprint

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOf_MatchesGitHashObject(t *testing.T) {
	// The claim in the package doc is that this is git's own scheme, so the
	// test is git itself. Anything else would only prove this agrees with a
	// second copy of its own arithmetic.
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH — the point of this test is agreeing with it")
	}

	for _, content := range []string{"", "hello\n", "package main\n\nfunc main() {}\n", strings.Repeat("x", 5000)} {
		path := filepath.Join(t.TempDir(), "f")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

		out, err := exec.Command(git, "hash-object", path).Output()
		require.NoError(t, err)
		want := strings.TrimSpace(string(out))

		assert.Equal(t, want, Of([]byte(content)), "content %q", truncate(content))

		got, err := OfFile(path)
		require.NoError(t, err)
		assert.Equal(t, want, got, "content %q", truncate(content))
	}
}

func TestOf_DerivesFromContentAlone(t *testing.T) {
	// identity_is_content. Same bytes at two paths written at different times
	// fingerprint the same; different bytes at one path do not.
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "nested", "b.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(b), 0o755))
	require.NoError(t, os.WriteFile(a, []byte("same\n"), 0o644))
	require.NoError(t, os.WriteFile(b, []byte("same\n"), 0o600))

	fa, err := OfFile(a)
	require.NoError(t, err)
	fb, err := OfFile(b)
	require.NoError(t, err)
	assert.Equal(t, fa, fb, "path and mode are not content")

	require.NoError(t, os.WriteFile(a, []byte("different\n"), 0o644))
	changed, err := OfFile(a)
	require.NoError(t, err)
	assert.NotEqual(t, fa, changed)
}

func TestOf_RevertedContentFingerprintsTheSame(t *testing.T) {
	// A file edited and put back has not become new. Written as its own test
	// because the skip depends on it: a revert must land back on the verdict
	// the original content already has.
	path := filepath.Join(t.TempDir(), "a.go")
	require.NoError(t, os.WriteFile(path, []byte("v1\n"), 0o644))
	first, err := OfFile(path)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(path, []byte("v2\n"), 0o644))
	require.NoError(t, os.WriteFile(path, []byte("v1\n"), 0o644))
	back, err := OfFile(path)
	require.NoError(t, err)

	assert.Equal(t, first, back)
}

func TestOfFile_MissingIsAnErrorNotAnEmptyHash(t *testing.T) {
	// Returning "" for an unreadable file would make every unreadable file
	// compare equal to every other, which is the one wrong answer that grants
	// an exemption.
	_, err := OfFile(filepath.Join(t.TempDir(), "nope.go"))
	require.Error(t, err)
}

func TestOfFile_DirectoryIsAnError(t *testing.T) {
	_, err := OfFile(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}

func truncate(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}

func TestOfFile_AFileThatChangesWhileBeingReadHasNoFingerprint(t *testing.T) {
	// The header commits to a length BEFORE the bytes are read, so a file that
	// changes underneath produces a hash matching neither what was there nor what
	// is now. Returning it would be worse than returning nothing: a wrong hash
	// that happens to match a stored pass exempts the file, which is a rule
	// silently not firing — the failure mode this whole package exists to avoid.
	//
	// Staged as a real race, because there is no seam between the stat and the
	// read to hook: OfFile stats the handle it already opened. The file is made
	// large enough that io.Copy needs many reads, and is truncated while they are
	// happening.
	//
	// An attempt where the copy finished before the truncation landed proves
	// nothing and is retried. But retries running out is a FAILURE, not a skip:
	// with the guard removed every attempt returns a hash, so twenty quiet
	// successes is exactly what the defect looks like. An earlier version of this
	// test skipped there and the mutant walked straight through it.
	dir := t.TempDir()

	const attempts = 20
	for attempt := range attempts {
		path := filepath.Join(dir, fmt.Sprintf("big-%d", attempt))
		require.NoError(t, os.WriteFile(path, make([]byte, 64<<20), 0o644))

		done := make(chan struct{})
		go func() {
			defer close(done)
			time.Sleep(time.Millisecond)
			os.Truncate(path, 0)
		}()

		fp, err := OfFile(path)
		<-done
		os.Remove(path)

		if err == nil {
			// Either the copy won the race — a correct answer about content that
			// really was there — or the guard is gone. The loop cannot tell them
			// apart, so it retries; running out is what tells them apart.
			continue
		}
		assert.Empty(t, fp, "a file that changed under the read must yield no fingerprint at all")
		assert.Contains(t, err.Error(), "changed while being read",
			"and the reason must name the race rather than something incidental")
		return
	}
	t.Fatalf("%d truncations mid-read and every one produced a fingerprint — the short-read guard is not firing", attempts)
}
