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

// checksEnv points the store at a scratch session and returns the workspace.
//
// The session and workspace come from the environment because they are facts
// about where a plugin runs, not names it picks — the same reason the command
// refuses to take them as flags.
// The workspace is a real REPOSITORY, and that is not scene-setting.
//
// State is keyed by the workspace's git root — a session is about a tree, and
// two callers standing in different subdirectories of one repo must reach the
// same record. A bare temp directory has no root, so the anchor walks UP until
// it finds one, and every test lands in whatever repository the test binary
// itself is running inside: one shared database, tests overwriting each other's
// registers, and failures that look like the command losing rows.
//
// Measured while writing these: without `git init` the per-owner test read back
// an empty register because a sibling test's environment had already claimed the
// same anchor.
func checksEnv(t *testing.T) string {
	t.Helper()
	ws := gitRepo(t, map[string]string{".keep": ""})
	t.Setenv("SR_WORKSPACE", ws)
	t.Setenv("SR_SESSION_ID", "s-"+t.Name())
	t.Setenv("XDG_DATA_HOME", tempDir(t))
	return ws
}

func runChecks(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := newRoot()
	root.SetArgs(append([]string{"checks"}, args...))
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	// Executed BEFORE the buffer is read. `return out.String(), root.Execute()`
	// evaluates left to right, so it reads an empty buffer and every assertion
	// about output silently sees "".
	err := root.Execute()
	return out.String(), err
}

func write(t *testing.T, ws, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(ws, name), []byte(body), 0o644))
}

func outstandingPaths(t *testing.T, owner string) []string {
	t.Helper()
	out, err := runChecks(t, "outstanding", "--owner", owner)
	require.NoError(t, err)
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row))
		paths = append(paths, row["path"].(string))
	}
	return paths
}

// The whole point, in one test: a refusal OUTLIVES the turn that produced it.
//
// A rule that objects once and forgets is not a rule — the agent is told no, the
// turn ends, and the next turn starts clean with the file still wrong. What
// makes the refusal mean anything is that it keeps being reported until fixed,
// and that a FIX ends the reporting rather than the file being pinned forever by
// one bad version.
func TestChecks_ARefusalOutlivesTheTurnAndAFixClearsIt(t *testing.T) {
	ws := checksEnv(t)
	write(t, ws, "a.md", "bad")

	_, err := runChecks(t, "record", "--owner", "rubrics", "--passed=false", "a.md")
	require.NoError(t, err)
	assert.Equal(t, []string{"a.md"}, outstandingPaths(t, "rubrics"),
		"a recorded refusal must still be reported after the call that made it")

	// Edited but not re-judged: still outstanding. The rule has not seen the new
	// content, so it cannot yet be said to be satisfied.
	write(t, ws, "a.md", "good")
	assert.Equal(t, []string{"a.md"}, outstandingPaths(t, "rubrics"),
		"an edit alone must not clear a refusal — nothing has judged the new content")

	_, err = runChecks(t, "record", "--owner", "rubrics", "--passed", "a.md")
	require.NoError(t, err)
	assert.Empty(t, outstandingPaths(t, "rubrics"),
		"passing at new content must end the reporting, or a fix could never finish")
}

// Each plugin has its OWN register. Two plugins judging one file hold two
// verdicts, and neither can clear or see the other's.
//
// Without this a newly installed plugin would inherit somebody else's pass and
// judge nothing, and one plugin's refusal would fail another plugin's turn.
func TestChecks_RegistersAreSeparatePerOwner(t *testing.T) {
	ws := checksEnv(t)
	write(t, ws, "a.md", "x")

	_, err := runChecks(t, "record", "--owner", "rubrics", "--passed=false", "a.md")
	require.NoError(t, err)

	assert.Equal(t, []string{"a.md"}, outstandingPaths(t, "rubrics"))
	assert.Empty(t, outstandingPaths(t, "secrets"),
		"one plugin's refusal must not appear in another's register")

	// And the other owner has not been given a pass either: it simply has no
	// record, so it must judge the file itself.
	_, err = runChecks(t, "skip", "--owner", "secrets", "a.md")
	assert.Error(t, err, "an owner with no record must not skip a file")
}

// Only content this owner PASSED is skippable. Never-judged and refused content
// are both "not skippable", deliberately the same answer: each means there is
// work to do.
func TestChecks_OnlyPassedContentIsSkippable(t *testing.T) {
	ws := checksEnv(t)
	write(t, ws, "a.md", "x")

	_, err := runChecks(t, "skip", "--owner", "rubrics", "a.md")
	assert.Error(t, err, "never judged is not skippable")

	_, err = runChecks(t, "record", "--owner", "rubrics", "--passed=false", "a.md")
	require.NoError(t, err)
	_, err = runChecks(t, "skip", "--owner", "rubrics", "a.md")
	assert.Error(t, err, "refused content must be judged again, or a fix could not be noticed")

	_, err = runChecks(t, "record", "--owner", "rubrics", "--passed", "a.md")
	require.NoError(t, err)
	_, err = runChecks(t, "skip", "--owner", "rubrics", "a.md")
	assert.NoError(t, err, "content this owner passed is skippable")
}

// The verdict is keyed by CONTENT, so changing the file re-opens the question
// and changing it BACK finds the answer already given.
//
// The second half is why the fingerprint is in the key rather than a column: an
// agent that edits a file and reverts it restores exactly the bytes an earlier
// judgement covered, and a register keyed by path alone would have thrown that
// judgement away.
func TestChecks_TheVerdictFollowsTheContent(t *testing.T) {
	ws := checksEnv(t)
	write(t, ws, "a.md", "first")

	_, err := runChecks(t, "record", "--owner", "rubrics", "--passed", "a.md")
	require.NoError(t, err)
	_, err = runChecks(t, "skip", "--owner", "rubrics", "a.md")
	require.NoError(t, err)

	write(t, ws, "a.md", "second")
	_, err = runChecks(t, "skip", "--owner", "rubrics", "a.md")
	assert.Error(t, err, "new content is a new question")

	write(t, ws, "a.md", "first")
	_, err = runChecks(t, "skip", "--owner", "rubrics", "a.md")
	assert.NoError(t, err, "content reverted to what was already passed must not be re-judged")
}

// An owner is required. Without it the command cannot say whose register it is
// about, and guessing would silently merge two plugins' records.
func TestChecks_OwnerIsRequired(t *testing.T) {
	ws := checksEnv(t)
	write(t, ws, "a.md", "x")

	for _, args := range [][]string{
		{"skip", "a.md"},
		{"record", "a.md"},
		{"outstanding"},
	} {
		_, err := runChecks(t, args...)
		assert.Error(t, err, "%v must refuse without --owner", args)
	}
}

// A path is keyed workspace-RELATIVE, whichever spelling the caller used.
//
// This is what lets `sr-file changes` and `sr-file checks` be piped into each
// other: changes reports relative paths, and a register keyed absolutely would
// find no row for the path it was just handed.
func TestChecks_AbsoluteAndRelativeNameTheSameRow(t *testing.T) {
	ws := checksEnv(t)
	write(t, ws, "a.md", "x")

	_, err := runChecks(t, "record", "--owner", "rubrics", "--passed=false", filepath.Join(ws, "a.md"))
	require.NoError(t, err)

	assert.Equal(t, []string{"a.md"}, outstandingPaths(t, "rubrics"),
		"a verdict recorded by absolute path must be reported relative")

	_, err = runChecks(t, "record", "--owner", "rubrics", "--passed", "a.md")
	require.NoError(t, err)
	assert.Empty(t, outstandingPaths(t, "rubrics"),
		"the relative spelling must reach the same row the absolute one wrote")
}
