package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runChangesOn drives the command the way a hook does — payload on stdin — and
// returns the rows it printed.
func runChangesOn(t *testing.T, payload string) []map[string]any {
	t.Helper()
	var out, errBuf bytes.Buffer
	root := newRoot()
	root.SetArgs([]string{"changes"})
	root.SetIn(strings.NewReader(payload))
	root.SetOut(&out)
	root.SetErr(&errBuf)
	require.NoError(t, root.Execute())

	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row), "each line must be one JSON object: %q", line)
		rows = append(rows, row)
	}
	return rows
}

// tempDir is t.TempDir with its symlinks resolved.
//
// On macOS t.TempDir returns a path under /var, which is a symlink to
// /private/var — and git, asked about a tree, answers with the resolved
// spelling. Handing the unresolved one to the command makes every path it
// reports fail to sit under the workspace it was given, so the rows come back
// empty and the test reads as "the command found nothing" when the command is
// fine. Resolving here keeps the failure meaning what it says.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
}

// gitRepo is a real repository, because the delete path resolves its targets
// against a tree — a bare temp directory reports nothing, which is a property of
// the module rather than of this command.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := tempDir(t)
	for name, body := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init"},
	} {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		require.NoError(t, c.Run(), "git %v", args)
	}
	return dir
}

// inWorkspace stands the process in dir for the duration of the test.
//
// Needed by any case whose command names a RELATIVE target, because filemod
// resolves those against the process's directory — see
// TestChanges_RelativeCommandTargetsNeedTheWorkspaceCwd, which pins that limit
// deliberately. Tests in this file must not run in parallel while this exists.
func inWorkspace(t *testing.T, dir string) {
	t.Helper()
	restore, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(restore) })
}

func payloadFor(t *testing.T, cwd, tool string, input any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"cwd": cwd, "tool_name": tool, "tool_input": input})
	require.NoError(t, err)
	return string(raw)
}

// The path is reported RELATIVE to the workspace, which is the whole reason the
// command reads `cwd`. A rule selects on `memories/...` because its author
// cannot know where the repo is checked out; Claude Code sends `file_path`
// absolute, so a command that passed it through would emit a spelling no
// selector matches — silently, since a selector that matches nothing looks
// exactly like a turn that touched nothing.
func TestChanges_ReportsThePathRelativeToTheWorkspace(t *testing.T) {
	dir := tempDir(t)
	abs := filepath.Join(dir, "memories", "tasks", "g", "n", "TASK.md")

	rows := runChangesOn(t, payloadFor(t, dir, "Write", map[string]any{
		"file_path": abs,
		"content":   "body",
	}))

	require.Len(t, rows, 1)
	assert.Equal(t, "memories/tasks/g/n/TASK.md", rows[0]["path"],
		"an absolute path reaches no selector an author can write")
	assert.Equal(t, "PreFileCreate", rows[0]["kind"])
	assert.Equal(t, "body", rows[0]["content"])
}

// One command, two targets, two rows. This is the case a hook cannot redo for
// itself: the engine parses the command line and resolves what it names, where a
// script would be pattern-matching a string.
func TestChanges_OneCommandCanTouchSeveralFiles(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.md": "x", "b.md": "y"})
	inWorkspace(t, dir)

	rows := runChangesOn(t, payloadFor(t, dir, "Bash", map[string]any{
		"command": "rm a.md b.md",
	}))

	require.Len(t, rows, 2)
	paths := []string{rows[0]["path"].(string), rows[1]["path"].(string)}
	assert.ElementsMatch(t, []string{"a.md", "b.md"}, paths)
	for _, r := range rows {
		assert.Equal(t, "PreFileDelete", r["kind"])
	}
}

// The pair that every hand-rolled copy of this gets wrong. A derivable edit
// carries the post-edit bytes; an underivable one carries resultKnown false and
// NO result — and the two must stay distinguishable, because a rule reading an
// absent result as "" cannot tell "the engine could not work it out" from "the
// write empties the file".
func TestChanges_UpdateSaysWhetherTheResultIsKnown(t *testing.T) {
	dir := gitRepo(t, map[string]string{"t.md": "old\n"})
	inWorkspace(t, dir)

	derivable := runChangesOn(t, payloadFor(t, dir, "Edit", map[string]any{
		"file_path":  filepath.Join(dir, "t.md"),
		"old_string": "old",
		"new_string": "new",
	}))
	require.Len(t, derivable, 1)
	assert.Equal(t, "PreFileUpdate", derivable[0]["kind"])
	assert.Equal(t, true, derivable[0]["resultKnown"])
	assert.Equal(t, "new\n", derivable[0]["result"])

	underivable := runChangesOn(t, payloadFor(t, dir, "Bash", map[string]any{
		"command": `sed -i "" s/old/new/ t.md`,
	}))
	require.Len(t, underivable, 1)
	assert.Equal(t, "PreFileUpdate", underivable[0]["kind"])
	assert.Equal(t, false, underivable[0]["resultKnown"],
		"an underivable result must be flagged, not reported as an empty one")
}

// A payload touching no file prints nothing and still succeeds. Most of what
// happens in a session concerns files not at all, and a non-zero status there
// would make every such hook refuse.
func TestChanges_NoFileChangeIsSilentAndSucceeds(t *testing.T) {
	dir := tempDir(t)

	var out, errBuf bytes.Buffer
	root := newRoot()
	root.SetArgs([]string{"changes"})
	root.SetIn(strings.NewReader(payloadFor(t, dir, "Grep", map[string]any{"pattern": "foo"})))
	root.SetOut(&out)
	root.SetErr(&errBuf)

	require.NoError(t, root.Execute())
	assert.Empty(t, strings.TrimSpace(out.String()))
}

// Every row is one object on one line, so `while read` and `jq -c` both work.
// Asserted as a property of the stream rather than of any single row, because
// content carrying newlines is the case that would break it.
func TestChanges_EachRowIsOneLine(t *testing.T) {
	dir := tempDir(t)

	var out bytes.Buffer
	root := newRoot()
	root.SetArgs([]string{"changes"})
	root.SetIn(strings.NewReader(payloadFor(t, dir, "Write", map[string]any{
		"file_path": filepath.Join(dir, "a.md"),
		"content":   "one\ntwo\nthree\n",
	})))
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	require.NoError(t, root.Execute())

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	assert.Len(t, lines, 1, "embedded newlines must not split a row across lines")
}

// A command naming a RELATIVE target is seen only from inside the workspace.
//
// This pins a LIMIT rather than a feature, and it is the engine's limit, not
// this command's: filemod asks the filesystem with the spelling the command
// used, which resolves against the process's own directory. Measured on
// `sr-session pre-tool` with a rule bound to PreFileDelete — run from inside the
// workspace it refuses `rm a.md`, run from anywhere else it permits silently.
//
// Pinned in both directions so the day filemod resolves against its root, this
// test fails and says so, rather than the limit quietly outliving the fix.
//
// os.Chdir here would hide it. The working directory is process-wide and this
// command has no claim on it; see changes.go for why that was rejected.
func TestChanges_RelativeCommandTargetsNeedTheWorkspaceCwd(t *testing.T) {
	dir := gitRepo(t, map[string]string{"notes.md": "x"})
	payload := payloadFor(t, dir, "Bash", map[string]any{"command": "rm notes.md"})

	restore, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(restore) })

	// From inside: seen.
	require.NoError(t, os.Chdir(dir))
	rows := runChangesOn(t, payload)
	require.Len(t, rows, 1, "inside the workspace a relative target must be reported")
	assert.Equal(t, "PreFileDelete", rows[0]["kind"])
	assert.Equal(t, "notes.md", rows[0]["path"])

	// From elsewhere: not seen. The limit, stated.
	require.NoError(t, os.Chdir(tempDir(t)))
	assert.Empty(t, runChangesOn(t, payload),
		"a relative command target is invisible from outside the workspace — "+
			"if this now returns a row, filemod resolves against its root and the "+
			"limit documented in changes.go is gone")
}

// An ABSOLUTE target is seen from anywhere, which is what makes the limit above
// a limit rather than a total failure — and is why tool-driven writes
// (Write/Edit, which carry absolute file_path) are unaffected by it.
func TestChanges_AbsoluteCommandTargetsAreSeenFromAnywhere(t *testing.T) {
	dir := gitRepo(t, map[string]string{"notes.md": "x"})

	restore, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(restore) })
	require.NoError(t, os.Chdir(tempDir(t)))

	rows := runChangesOn(t, payloadFor(t, dir, "Bash", map[string]any{
		"command": "rm " + filepath.Join(dir, "notes.md"),
	}))

	require.Len(t, rows, 1)
	assert.Equal(t, "PreFileDelete", rows[0]["kind"])
	assert.Equal(t, "notes.md", rows[0]["path"], "the reported path is still workspace-relative")
}

// A payload that is not JSON is an error, not an empty result. Reporting nothing
// would be indistinguishable from a turn that touched no file, and a caller
// would read a broken pipe as a clean one.
func TestChanges_AMalformedPayloadIsAnError(t *testing.T) {
	root := newRoot()
	root.SetArgs([]string{"changes"})
	root.SetIn(strings.NewReader("not json"))
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	assert.Error(t, root.Execute())
}
