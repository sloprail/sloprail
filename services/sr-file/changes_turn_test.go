package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runChangesTurnOn(t *testing.T, ws string) []map[string]any {
	t.Helper()
	var out, errBuf bytes.Buffer
	root := newRoot()
	root.SetArgs([]string{"changes", "--turn"})
	root.SetOut(&out)
	root.SetErr(&errBuf)
	t.Setenv("SR_WORKSPACE", ws)
	require.NoError(t, root.Execute(), "stderr: %s", errBuf.String())

	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row))
		rows = append(rows, row)
	}
	return rows
}

func pathsOf(rows []map[string]any) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r["path"].(string)
	}
	return out
}

// The headline: --turn reports what differs from HEAD — NOT the whole tree.
// Untouched files must not appear, which is what makes this cheap enough to run
// on every Stop rather than a full-tree scan a glob then filters down from.
func TestChangesTurn_OnlyWhatDiffersFromHEAD(t *testing.T) {
	ws := gitRepo(t, map[string]string{"a.md": "x", "b.md": "y", "c.txt": "z"})
	inWorkspace(t, ws)

	// Nothing changed since the commit: no rows at all.
	assert.Empty(t, runChangesTurnOn(t, ws), "an untouched tree must report nothing")

	require.NoError(t, os.WriteFile(filepath.Join(ws, "a.md"), []byte("modified"), 0o644))
	rows := runChangesTurnOn(t, ws)
	assert.Equal(t, []string{"a.md"}, pathsOf(rows),
		"b.md and c.txt were not touched and must not appear")
}

// A file never committed — untracked — is reported the same as a modified
// tracked one. A rule about "what changed" must see a brand-new file; git
// itself treats tracked-vs-untracked as two different questions, and this
// command answers the ONE question a rule actually has.
func TestChangesTurn_UntrackedFilesAreReported(t *testing.T) {
	ws := gitRepo(t, map[string]string{".keep": ""})
	inWorkspace(t, ws)

	require.NoError(t, os.WriteFile(filepath.Join(ws, "new.md"), []byte("brand new"), 0o644))
	rows := runChangesTurnOn(t, ws)
	assert.Equal(t, []string{"new.md"}, pathsOf(rows))
}

// Each row carries a fingerprint of the file's CURRENT content, which is what
// lets a caller pipe straight into `sr-file checks skip` without a second read
// of the file.
func TestChangesTurn_RowsCarryAFingerprint(t *testing.T) {
	ws := gitRepo(t, map[string]string{"a.md": "original"})
	inWorkspace(t, ws)

	require.NoError(t, os.WriteFile(filepath.Join(ws, "a.md"), []byte("changed"), 0o644))
	rows := runChangesTurnOn(t, ws)
	require.Len(t, rows, 1)
	fp, ok := rows[0]["fingerprint"].(string)
	require.True(t, ok)
	assert.NotEmpty(t, fp)
}

// The kind is "Changed" — not one of the Pre/Post kinds this command's other
// mode reports. There is no create/update/delete distinction here on purpose:
// --turn answers "does this differ from history", and history does not
// distinguish "you just created this" from "you rewrote it entirely" the way a
// single tool call does.
func TestChangesTurn_KindIsChanged(t *testing.T) {
	ws := gitRepo(t, map[string]string{"a.md": "x"})
	inWorkspace(t, ws)
	require.NoError(t, os.WriteFile(filepath.Join(ws, "a.md"), []byte("y"), 0o644))

	rows := runChangesTurnOn(t, ws)
	require.Len(t, rows, 1)
	assert.Equal(t, "Changed", rows[0]["kind"])
}

// A path DELETED since the commit is not reported as a row with a fingerprint
// — there is no content left to fingerprint. It is named on stderr instead of
// silently vanishing from the report, so a caller can tell "nothing changed
// here" apart from "something changed here and this command could not say
// what".
func TestChangesTurn_ADeletedPathIsNamedNotSilentlyDropped(t *testing.T) {
	ws := gitRepo(t, map[string]string{"a.md": "x", "b.md": "y"})
	inWorkspace(t, ws)
	require.NoError(t, os.Remove(filepath.Join(ws, "a.md")))

	var out, errBuf bytes.Buffer
	root := newRoot()
	root.SetArgs([]string{"changes", "--turn"})
	root.SetOut(&out)
	root.SetErr(&errBuf)
	t.Setenv("SR_WORKSPACE", ws)
	require.NoError(t, root.Execute())

	assert.Empty(t, strings.TrimSpace(out.String()), "a deleted file has no content to report as a row")
	assert.Contains(t, errBuf.String(), "a.md", "the deleted path must be named, not silently dropped")
}
