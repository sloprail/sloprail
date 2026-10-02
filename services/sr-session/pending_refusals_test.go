package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/repochecks"
	"github.com/sloprail/sloprail/internal/sessionpath"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

// Refused work must never pass because its source is not migrated yet: a sibling session's old
// file that could not be imported (an older Stop is writing it) still counts.
func TestOutstandingRefusalBases_ASiblingOldFileNotYetImportedStillCounts(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	gitIn(t, root, "init", "-q", "-b", "main")
	gitIn(t, root, "commit", "-q", "--allow-empty", "-m", "base")
	base := gitIn(t, root, "rev-parse", "HEAD")
	gitIn(t, root, "commit", "-q", "--allow-empty", "-m", "refused work")
	bad := gitIn(t, root, "rev-parse", "HEAD")

	// The sibling's old file: a refusal of `bad`, and a run still RUNNING (an older Stop).
	sib, err := sessionpath.ChecksDB(root, "sibling")
	require.NoError(t, err)
	s, err := checkstore.Open(sib)
	require.NoError(t, err)
	id, err := s.RecordRun(checkstore.CheckRun{BatchID: "b", CheckID: "p/file-guard/x", BaseRef: base, HeadRef: bad,
		Metadata: map[string]any{"ruleHash": "r"}})
	require.NoError(t, err)
	_, err = s.RecordCheck(id, checkstore.CheckRecord{Subject: "changeset", Kind: "k", Status: checkstore.StatusFail})
	require.NoError(t, err)
	require.NoError(t, s.FinishRun(id))
	_, err = s.RecordRun(checkstore.CheckRun{BatchID: "b", CheckID: "p/file-guard/x", BaseRef: base, HeadRef: bad})
	require.NoError(t, err) // RUNNING: postpones the import
	require.NoError(t, s.Close())

	own, err := repochecks.Open(root, "me", nil)
	require.NoError(t, err)
	defer own.Close()
	bases, err := outstandingRefusalBases(root, "p/file-guard/x", newFamilyResults(own))
	require.NoError(t, err)
	assert.Equal(t, []string{base}, bases, "the sibling's refusal counts although its file is not migrated")
}
