package declaration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const protectedName = "sloprail/file-guard/grounded-rule-changes"

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

// A protected rule's `disabled:` entry counts only when the config committed at the trusted
// commit lists it: the working tree, and the agent's own later commits, cannot switch it off.
func TestTrustProtected(t *testing.T) {
	repo := t.TempDir()
	root := filepath.Join(repo, ".sloprail")
	require.NoError(t, os.MkdirAll(root, 0o755))
	gitIn(t, repo, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("disabled:\n  - sloprail/file-guard/other\n"), 0o644))
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "start")
	startOut, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	start := string(startOut[:len(startOut)-1])

	// The agent writes the protected name, then commits it.
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("disabled:\n  - sloprail/file-guard/other\n  - "+protectedName+"\n"), 0o644))
	cfg, err := loadConfig(root)
	require.NoError(t, err)
	assert.NotContains(t, trustProtected(cfg, root, start).Disabled, protectedName, "the working tree must not disable it")
	assert.NotContains(t, trustProtected(cfg, root, "").Disabled, protectedName, "nor an uncommitted entry judged against HEAD")
	assert.Contains(t, trustProtected(cfg, root, start).Disabled, "sloprail/file-guard/other", "other rules are unaffected")

	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "agent disables it")
	assert.NotContains(t, trustProtected(cfg, root, start).Disabled, protectedName, "nor the agent's own commit, judged against the session start")

	// A user who committed it before the session began did switch it off.
	assert.Contains(t, trustProtected(cfg, root, "HEAD").Disabled, protectedName)

	// Not a repository: nothing protected is honoured.
	assert.NotContains(t, trustProtected(cfg, t.TempDir(), "").Disabled, protectedName)
}
