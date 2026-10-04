package ruletest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/module/modules"
)

// repoWith makes a git repository holding the files, committed.
func repoWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := c.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("init", "-q", "-b", "main")
	for f, body := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644))
	}
	run("add", "-A")
	run("commit", "-q", "-m", "x")
	return dir
}

func build(t *testing.T, repo, kind string, fields map[string]any) (map[string]any, error) {
	t.Helper()
	reg, err := modules.Registry()
	require.NoError(t, err)
	ev, err := BuildEvent(reg, kind, fields, repo)
	if err != nil {
		return nil, err
	}
	require.Equal(t, kind, ev.Kind)
	return ev.Fields, nil
}

func TestBuildEvent_ACreateCarriesWhatTheModuleDerives(t *testing.T) {
	f, err := build(t, t.TempDir(), "PreFileCreate", map[string]any{
		"path": "a.md", "newContent": "hello\n# sr:asked \"why\"\n",
	})
	require.NoError(t, err)
	require.Equal(t, "a.md", f["path"])
	require.Equal(t, true, f["resultKnown"])
	require.Len(t, f["newMarkers"], 1, "the markers of the new text are derived, not written by hand")
	require.Equal(t, []any{}, f["citations"])
}

func TestBuildEvent_AnUpdateReadsItsOldBytesOffTheDisk(t *testing.T) {
	repo := repoWith(t, map[string]string{"a.md": "old\n"})
	f, err := build(t, repo, "PreFileUpdate", map[string]any{"path": "a.md", "newContent": "new\n"})
	require.NoError(t, err)
	require.Equal(t, "old\n", f["oldContent"])
	require.Equal(t, "new\n", f["newContent"])
}

func TestBuildEvent_PreFileWriteChoosesCreateOrUpdate(t *testing.T) {
	repo := repoWith(t, map[string]string{"a.md": "old\n"})
	reg, err := modules.Registry()
	require.NoError(t, err)
	ev, err := BuildEvent(reg, "PreFileWrite", map[string]any{"path": "a.md", "newContent": "n"}, repo)
	require.NoError(t, err)
	require.Equal(t, "PreFileUpdate", ev.Kind)
	ev, err = BuildEvent(reg, "PreFileWrite", map[string]any{"path": "b.md", "newContent": "n"}, repo)
	require.NoError(t, err)
	require.Equal(t, "PreFileCreate", ev.Kind)
}

func TestBuildEvent_AWriteTheEngineCouldNotComputeIsSaidOutright(t *testing.T) {
	repo := repoWith(t, map[string]string{"a.md": "old\n"})
	f, err := build(t, repo, "PreFileUpdate", map[string]any{"path": "a.md", "resultKnown": false})
	require.NoError(t, err)
	require.Equal(t, false, f["resultKnown"])
	require.Equal(t, "", f["newContent"])

	// ... and a write that simply forgot its bytes is an error, not a silent empty file.
	_, err = build(t, repo, "PreFileUpdate", map[string]any{"path": "a.md"})
	require.ErrorContains(t, err, "needs `newContent:`")
}

func TestBuildEvent_ADeleteReadsWhatIsAboutToBeLost(t *testing.T) {
	repo := repoWith(t, map[string]string{"a.md": "precious\n"})
	f, err := build(t, repo, "PreFileDelete", map[string]any{"path": "a.md"})
	require.NoError(t, err)
	require.Equal(t, "precious\n", f["oldContent"])
	require.Equal(t, true, f["oldContentKnown"])
}

func TestBuildEvent_APostEventIsTheSettledFile(t *testing.T) {
	repo := repoWith(t, map[string]string{"a.md": "settled\n"})
	f, err := build(t, repo, "PostFileCreate", map[string]any{"path": "a.md"})
	require.NoError(t, err)
	require.Equal(t, "settled\n", f["newContent"])
	require.Equal(t, true, f["newContentKnown"])
	_, err = build(t, repo, "PostFileCreate", map[string]any{"path": "missing.md"})
	require.ErrorContains(t, err, "not in the repository")
}

func TestBuildEvent_ACommandIsParsedByTheEnginesOwnParser(t *testing.T) {
	f, err := build(t, t.TempDir(), "PreCommandInvoke", map[string]any{"command": "cd x && git push --force origin main"})
	require.NoError(t, err)
	require.Equal(t, "cd x && git push --force origin main", f["raw"])
	invs := f["invocations"].([]any)
	require.GreaterOrEqual(t, len(invs), 2)
	_, err = build(t, t.TempDir(), "PreCommandInvoke", map[string]any{})
	require.ErrorContains(t, err, "needs `command:`")
}

func TestBuildEvent_Tags(t *testing.T) {
	f, err := build(t, t.TempDir(), "PostTagWrite", map[string]any{"tags": []any{"research", map[string]any{"label": "#skip", "seen": true}}})
	require.NoError(t, err)
	tags := f["tags"].([]any)
	require.Equal(t, map[string]any{"label": "research", "seen": false}, tags[0])
	require.Equal(t, map[string]any{"label": "skip", "seen": true}, tags[1])
}

func TestBuildEvent_ToolAndStop(t *testing.T) {
	f, err := build(t, t.TempDir(), "PreToolUse", map[string]any{"tool": "WebFetch", "input": map[string]any{"url": "https://x"}})
	require.NoError(t, err)
	require.Equal(t, "WebFetch", f["tool"])
	f, err = build(t, t.TempDir(), "Stop", nil)
	require.NoError(t, err)
	require.Empty(t, f)
}

func TestBuildEvent_RefusesWhatTheKindDoesNotCarry(t *testing.T) {
	_, err := build(t, t.TempDir(), "PreFileCreate", map[string]any{"path": "a", "newContnet": "x"})
	require.ErrorContains(t, err, `no field "newContnet"`)
	_, err = build(t, t.TempDir(), "PreFileMake", map[string]any{})
	require.ErrorContains(t, err, "unknown event kind")
	require.ErrorContains(t, err, "PreFileCreate")
}
