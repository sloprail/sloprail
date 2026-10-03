package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepoID(t *testing.T) {
	tmpDir := t.TempDir()

	runGitT(t, tmpDir, "init")
	runGitT(t, tmpDir, "config", "user.email", "test@example.com")
	runGitT(t, tmpDir, "config", "user.name", "Test User")

	testFile := filepath.Join(tmpDir, "test.txt")
	err := os.WriteFile(testFile, []byte("test content"), 0644)
	require.NoError(t, err)

	runGitT(t, tmpDir, "add", ".")
	runGitT(t, tmpDir, "commit", "-m", "init")

	// Test 1: No remote — fingerprint is the realpath of .git dir.
	fingerprint, err := RepoID(tmpDir)
	require.NoError(t, err)
	// Must contain the .git path and be non-empty.
	assert.NotEmpty(t, string(fingerprint))
	assert.True(t, strings.HasSuffix(string(fingerprint), ".git"),
		"no-remote fingerprint should end with .git, got: %s", fingerprint)

	// Test 2: Add remote — fingerprint should be normalize(remote):initial_sha.
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = tmpDir
	out, err := cmd.Output()
	require.NoError(t, err)
	initialSHA := strings.TrimSpace(string(out))

	runGitT(t, tmpDir, "remote", "add", "origin", "https://github.com/Org/Repo.git")
	fingerprint, err = RepoID(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, "github.com/org/repo:"+initialSHA, fingerprint)

	// Test 3: Call from subdirectory gives same fingerprint.
	subDir := filepath.Join(tmpDir, "subdir")
	err = os.MkdirAll(subDir, 0755)
	require.NoError(t, err)

	fingerprintFromSubdir, err := RepoID(subDir)
	require.NoError(t, err)
	assert.Equal(t, fingerprint, fingerprintFromSubdir)
}

func TestRepoID_TwoLocalReposAreDifferent(t *testing.T) {
	// Two local repos with no remote must get different fingerprints,
	// even if they have identical (empty-tree) initial commits.
	makeRepo := func() string {
		dir := t.TempDir()
		runGitT(t, dir, "init", "-b", "main")
		runGitT(t, dir, "config", "user.email", "t@t.com")
		runGitT(t, dir, "config", "user.name", "T")
		runGitT(t, dir, "commit", "--allow-empty", "-m", "init")
		return dir
	}

	repo1 := makeRepo()
	repo2 := makeRepo()

	fp1, err := RepoID(repo1)
	require.NoError(t, err)
	fp2, err := RepoID(repo2)
	require.NoError(t, err)

	assert.NotEqual(t, fp1, fp2, "two distinct local repos must have different fingerprints")
}

// runGit is a helper that runs a git command and fails the test on error.
func runGitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
