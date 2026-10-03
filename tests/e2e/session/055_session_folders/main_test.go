package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// session_folders: the repositories a session works in — its own, a sub-agent's worktree,
// and any repository a command runs in (`git -C ../other commit`, `cd ../other && …`) — are
// registered, and each folder's OWN .sloprail rules apply there: its gates judge the calls
// made in it, and commit-required at Stop covers its uncommitted work. Nothing about a folder
// decides which commits a file-guard judges: `sr-checks run` is given its range.

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

var (
	Turns = harness.Turns
	Bash  = harness.Bash
)

type Env = harness.Env

func real(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// two is a session's own project and another repository, each a committed repository.
func two(t *testing.T) (*Env, string, string) {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.WithSubagentStopCheck())
	proj, other := e.Project(), e.Project()
	e.GitInit(proj)
	e.GitInit(other)
	return e, proj, other
}

func folderAt(folders []sessionstate.Folder, path string) (sessionstate.Folder, bool) {
	for _, f := range folders {
		if f.Path == path {
			return f, true
		}
	}
	return sessionstate.Folder{}, false
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}
