package changeset

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/gitrepo"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "--initial-branch=main")
	git(t, dir, "config", "user.email", "test@example.invalid")
	git(t, dir, "config", "user.name", "Test")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// put writes files and commits them with msg, returning the new HEAD.
func put(t *testing.T, dir, msg string, files map[string]string) string {
	t.Helper()
	for rel, body := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644))
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

var markerRE = regexp.MustCompile(`sr:(\w+) (\S+)`)

func scan(text string) []Marker {
	var out []Marker
	for i, line := range strings.Split(text, "\n") {
		if m := markerRE.FindStringSubmatch(line); m != nil {
			out = append(out, Marker{Kind: m[1], FQN: m[2], Line: i + 1})
		}
	}
	return out
}

func selectAll(Scope) (bool, error) { return true, nil }

func rng(base, head string) gitrepo.Range { return gitrepo.Range{Base: base, Head: head} }
