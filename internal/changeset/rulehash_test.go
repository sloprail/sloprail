package changeset

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ruleFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "file-guard.yaml", "match: '*.go'\n", 0o644)
	write(t, dir, "check.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, dir, "rubric.md.j2", "judge this\n", 0o644)
	return dir
}

func write(t *testing.T, dir, rel, body string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte(body), mode))
	require.NoError(t, os.Chmod(filepath.Join(dir, rel), mode))
}

func hash(t *testing.T, dir string) string {
	t.Helper()
	h, err := RuleHash(dir)
	require.NoError(t, err)
	return h
}

func TestRuleHash_IsDeterministicAndPathIndependent(t *testing.T) {
	a, b := ruleFolder(t), ruleFolder(t)
	assert.Equal(t, hash(t, a), hash(t, a))
	assert.Equal(t, hash(t, a), hash(t, b), "two folders with the same contents are the same rule")
}

func TestRuleHash_AnyFileInTheFolderChangesIt(t *testing.T) {
	for name, edit := range map[string]func(dir string){
		"yaml":      func(d string) { write(t, d, "file-guard.yaml", "match: '*.ts'\n", 0o644) },
		"script":    func(d string) { write(t, d, "check.sh", "#!/bin/sh\nexit 1\n", 0o755) },
		"template":  func(d string) { write(t, d, "rubric.md.j2", "judge harder\n", 0o644) },
		"new file":  func(d string) { write(t, d, "extra.txt", "x", 0o644) },
		"nested":    func(d string) { write(t, d, "lib/helper.sh", "x", 0o644) },
		"removed":   func(d string) { require.NoError(t, os.Remove(filepath.Join(d, "rubric.md.j2"))) },
		"exec bit":  func(d string) { require.NoError(t, os.Chmod(filepath.Join(d, "check.sh"), 0o644)) },
		"empty dir": func(d string) { require.NoError(t, os.Mkdir(filepath.Join(d, "empty"), 0o755)) },
	} {
		dir := ruleFolder(t)
		before := hash(t, dir)
		edit(dir)
		assert.NotEqual(t, before, hash(t, dir), "editing the %s must change the hash", name)
	}
}

func TestRuleHash_ContentAndNamesCannotBeRecut(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "ab", "c", 0o644)
	write(t, b, "a", "bc", 0o644)
	assert.NotEqual(t, hash(t, a), hash(t, b))
}

func TestRuleHash_ASymlinkIsItsTargetNotWhatItPointsAt(t *testing.T) {
	dir := ruleFolder(t)
	write(t, dir, "one.txt", "same", 0o644)
	write(t, dir, "two.txt", "same", 0o644)
	require.NoError(t, os.Symlink("one.txt", filepath.Join(dir, "link")))
	before := hash(t, dir)
	require.NoError(t, os.Remove(filepath.Join(dir, "link")))
	require.NoError(t, os.Symlink("two.txt", filepath.Join(dir, "link")))
	assert.NotEqual(t, before, hash(t, dir))
}

func TestRuleHash_DSStoreIsNotPartOfTheRule(t *testing.T) {
	dir := ruleFolder(t)
	before := hash(t, dir)
	write(t, dir, ".DS_Store", "finder noise", 0o644)
	assert.Equal(t, before, hash(t, dir))
}

func TestRuleHash_AnUnreadableFileIsAnErrorNotAPartialHash(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	dir := ruleFolder(t)
	require.NoError(t, os.Chmod(filepath.Join(dir, "rubric.md.j2"), 0))
	t.Cleanup(func() { os.Chmod(filepath.Join(dir, "rubric.md.j2"), 0o644) })
	_, err := RuleHash(dir)
	assert.Error(t, err)
}

func TestRuleHash_MissingOrNonFolderIsAnError(t *testing.T) {
	_, err := RuleHash(filepath.Join(t.TempDir(), "absent"))
	assert.Error(t, err)
	f := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
	_, err = RuleHash(f)
	assert.Error(t, err)
}

func hashAt(t *testing.T, repo, dir string) string {
	t.Helper()
	h, err := RuleHashAt(repo, dir, false)
	require.NoError(t, err)
	return h
}

// What is hashed is what runs, restricted to tracked files: a file a check writes into its
// own folder (a ledger) is not the rule changing; an uncommitted edit of a tracked script is.
func TestRuleHashAt_TrackedFilesAsOnDisk(t *testing.T) {
	repo := initRepo(t)
	dir := filepath.Join(repo, ".sloprail", "file-guard", "r")
	put(t, repo, "rule", map[string]string{".sloprail/file-guard/r/check.sh": "#!/bin/sh\nexit 0\n", "other.txt": "1"})
	before := hashAt(t, repo, dir)

	write(t, dir, "ledger", "run\n", 0o644)
	assert.Equal(t, before, hashAt(t, repo, dir), "an untracked file written by a check is not the rule")
	require.NoError(t, os.Remove(filepath.Join(dir, "ledger")))

	put(t, repo, "unrelated", map[string]string{"other.txt": "2"})
	assert.Equal(t, before, hashAt(t, repo, dir), "a commit elsewhere does not move it")

	write(t, dir, "check.sh", "#!/bin/sh\nexit 1\n", 0o644)
	assert.NotEqual(t, before, hashAt(t, repo, dir), "an uncommitted edit of a tracked script is a different rule")
}

func TestRuleHashAt_NewRuleHashesItsUnignoredFiles(t *testing.T) {
	repo := initRepo(t)
	put(t, repo, "base", map[string]string{"other.txt": "1"})
	dir := filepath.Join(repo, ".sloprail", "file-guard", "new")
	write(t, dir, "check.sh", "#!/bin/sh\n", 0o755)
	before := hashAt(t, repo, dir)
	assert.Equal(t, before, hashAt(t, repo, dir))
	write(t, dir, "check.sh", "#!/bin/sh\nexit 1\n", 0o755)
	assert.NotEqual(t, before, hashAt(t, repo, dir), "an edit of the new rule's own file is seen")
}

// A shared file under the guard's .sloprail root but outside its folder (a `_lib` script the
// guard sources) is part of the rule: its tracked edit changes the hash; an untracked ledger
// anywhere under the root does not.
func TestRuleHashAt_ATrackedSharedFileUnderTheRootChangesIt(t *testing.T) {
	repo := initRepo(t)
	dir := filepath.Join(repo, ".sloprail", "file-guard", "r")
	put(t, repo, "rule", map[string]string{
		".sloprail/file-guard/r/check.sh": "#!/bin/sh\n. ../../_lib/x.sh\n",
		".sloprail/_lib/x.sh":             "echo 1\n",
	})
	before := hashAt(t, repo, dir)

	write(t, filepath.Join(repo, ".sloprail", "_lib"), "ledger", "run\n", 0o644)
	assert.Equal(t, before, hashAt(t, repo, dir), "an untracked file under the root is not the rule")

	write(t, filepath.Join(repo, ".sloprail", "_lib"), "x.sh", "echo 2\n", 0o644)
	assert.NotEqual(t, before, hashAt(t, repo, dir), "a tracked _lib edit is a different rule")
}

func TestRuleHashAt_SymlinkedRepoPathIsTheSameRule(t *testing.T) {
	repo := initRepo(t)
	dir := filepath.Join(repo, ".sloprail", "file-guard", "r")
	put(t, repo, "rule", map[string]string{".sloprail/file-guard/r/check.sh": "x"})
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(repo, link))
	assert.Equal(t, hashAt(t, repo, dir), hashAt(t, link, filepath.Join(link, ".sloprail", "file-guard", "r")))
	assert.Equal(t, hashAt(t, repo, dir), hashAt(t, link, dir), "repo through a link, dir real")
}

func TestRuleHashAt_FailsClosed(t *testing.T) {
	repo := initRepo(t)
	put(t, repo, "base", map[string]string{"other.txt": "1"})
	_, err := RuleHashAt(repo, ruleFolder(t), false)
	assert.Error(t, err, "a non-plugin rule outside the repository")
	_, err = RuleHashAt(t.TempDir(), ruleFolder(t), false)
	assert.Error(t, err, "not a repository")
}

func TestRuleHashAt_PluginFolderIsStable(t *testing.T) {
	dir := ruleFolder(t)
	a, err := RuleHashAt(t.TempDir(), dir, true)
	require.NoError(t, err)
	b, err := RuleHashAt(t.TempDir(), dir, true)
	require.NoError(t, err)
	assert.Equal(t, a, b)
}
