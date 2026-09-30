package gitrepo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commitFile writes one file and commits it, returning HEAD.
func commitFile(t *testing.T, dir, path, body, msg string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, path), []byte(body), 0o644))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

func TestFileCommits_AFileIsChangedByTheCommitsThatTouchedIt(t *testing.T) {
	dir := initRepo(t)
	base := commitFile(t, dir, "a.md", "a\n", "seed")
	c1 := commitFile(t, dir, "a.md", "a1\n", "a")
	c2 := commitFile(t, dir, "b.md", "b\n", "b")
	c3 := commitFile(t, dir, "a.md", "a2\n", "a again")

	got, err := FileCommits(dir, base, c3, []FileRef{{Path: "a.md"}, {Path: "b.md"}})
	require.NoError(t, err)
	assert.Equal(t, []string{c1, c3}, got["a.md"], "oldest first")
	assert.Equal(t, []string{c2}, got["b.md"])
}

func TestFileCommits_ARenameIsFollowedBackThroughTheRange(t *testing.T) {
	dir := initRepo(t)
	body := "a body long enough to be a rename\nsecond line\nthird line\n"
	base := commitFile(t, dir, "m/x.md", body, "seed")
	c1 := commitFile(t, dir, "m/x.md", body+"edit\n", "edit before the move")
	git(t, dir, "mv", "m/x.md", "z.md")
	git(t, dir, "commit", "-m", "move")
	c2 := git(t, dir, "rev-parse", "HEAD")
	c3 := commitFile(t, dir, "z.md", body+"edit\nmore\n", "edit after the move")
	git(t, dir, "mv", "z.md", "final.md")
	git(t, dir, "commit", "-m", "move again")
	c4 := git(t, dir, "rev-parse", "HEAD")

	got, err := FileCommits(dir, base, c4, []FileRef{{Path: "final.md", OldPath: "m/x.md"}})
	require.NoError(t, err)
	assert.Equal(t, []string{c1, c2, c3, c4}, got["final.md"], "the commits under the old names count, through two moves")
}

func TestFileCommits_ADeletionIsAChange(t *testing.T) {
	dir := initRepo(t)
	base := commitFile(t, dir, "a.md", "a\n", "seed")
	git(t, dir, "rm", "a.md")
	git(t, dir, "commit", "-m", "remove")
	head := git(t, dir, "rev-parse", "HEAD")

	got, err := FileCommits(dir, base, head, []FileRef{{Path: "a.md"}})
	require.NoError(t, err)
	assert.Equal(t, []string{head}, got["a.md"])
}

func TestFileCommits_ASideBranchsCommitIsTheChangeNotTheMerge(t *testing.T) {
	dir := initRepo(t)
	base := commitFile(t, dir, "a.md", "a\n", "seed")
	git(t, dir, "checkout", "-b", "side")
	side := commitFile(t, dir, "s.md", "s\n", "side work")
	git(t, dir, "checkout", "main")
	commitFile(t, dir, "a.md", "a1\n", "main work")
	git(t, dir, "merge", "--no-ff", "side", "-m", "merge")
	head := git(t, dir, "rev-parse", "HEAD")

	got, err := FileCommits(dir, base, head, []FileRef{{Path: "s.md"}})
	require.NoError(t, err)
	assert.Equal(t, []string{side}, got["s.md"], "the merge brought it in and changed nothing itself")
}

func TestFileCommits_ARangeFromBeforeTheFirstCommitCoversTheRootCommit(t *testing.T) {
	dir := initRepo(t)
	root := commitFile(t, dir, "a.md", "a\n", "root")
	got, err := FileCommits(dir, EmptyTree, root, []FileRef{{Path: "a.md"}})
	require.NoError(t, err)
	assert.Equal(t, []string{root}, got["a.md"])
}

func TestFileCommits_AnUnknownCommitIsAnError(t *testing.T) {
	dir := initRepo(t)
	commitFile(t, dir, "a.md", "a\n", "root")
	_, err := FileCommits(dir, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "HEAD", []FileRef{{Path: "a.md"}})
	assert.Error(t, err)
}

func TestParseNameStatusLog_RefusesWhatItDoesNotUnderstand(t *testing.T) {
	_, err := parseNameStatusLog("\x01nothash\x00M\x00a.md\x00")
	assert.Error(t, err)
	_, err = parseNameStatusLog("\x01" + EmptyTree + "\x00Q\x00a.md\x00")
	assert.Error(t, err)
	_, err = parseNameStatusLog("\x01" + EmptyTree + "\x00R100\x00only-one\x00")
	assert.Error(t, err)
}
