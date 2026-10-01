package gitrepo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommitsIn_OldestFirstWithSubjectBodyAndTrailers(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "seed.txt", "s")

	commit(t, dir, "a.txt", "1")
	git(t, dir, "commit", "--amend", "-qm", "first change\n\nthe body of it\n\nSloprail-Cites-User: split the runner files\nSloprail-Refactor: move-only")
	first := git(t, dir, "rev-parse", "HEAD")
	commit(t, dir, "b.txt", "2")
	second := git(t, dir, "rev-parse", "HEAD")

	cs, err := CommitsIn(dir, base, second)
	require.NoError(t, err)
	require.Len(t, cs, 2)
	assert.Equal(t, first, cs[0].SHA)
	assert.Equal(t, "first change", cs[0].Subject)
	assert.Contains(t, cs[0].Body, "the body of it")
	assert.Equal(t, []Trailer{
		{Key: "Sloprail-Cites-User", Value: "split the runner files"},
		{Key: "Sloprail-Refactor", Value: "move-only"},
	}, cs[0].Trailers)
	assert.Equal(t, second, cs[1].SHA)
	assert.Empty(t, cs[1].Trailers)
}

func TestCommitsIn_UnfoldsAWrappedTrailerValue(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "seed.txt", "s")
	commit(t, dir, "a.txt", "1")
	git(t, dir, "commit", "--amend", "-qm", "subject\n\nSloprail-Cites-User: a quote that\n wraps onto a second line")
	cs, err := CommitsIn(dir, base, git(t, dir, "rev-parse", "HEAD"))
	require.NoError(t, err)
	require.Len(t, cs, 1)
	require.Len(t, cs[0].Trailers, 1)
	assert.Equal(t, "a quote that wraps onto a second line", cs[0].Trailers[0].Value)
}

func TestCommitsIn_ProseColonIsNotATrailer(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "seed.txt", "s")
	commit(t, dir, "a.txt", "1")
	git(t, dir, "commit", "--amend", "-qm", "subject\n\nNote: this is prose\nmore prose after it\n\nnot a trailer block either")
	cs, err := CommitsIn(dir, base, git(t, dir, "rev-parse", "HEAD"))
	require.NoError(t, err)
	assert.Empty(t, cs[0].Trailers)
}

func TestCommitsIn_EmptyRangeHasNoCommits(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "1")
	cs, err := CommitsIn(dir, head, head)
	require.NoError(t, err)
	assert.Empty(t, cs)
}

func TestCommitsIn_AnUnknownBaseIsAnError(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "1")
	_, err := CommitsIn(dir, "0123456789012345678901234567890123456789", head)
	assert.Error(t, err)
}

func TestCommitsIn_IncludesMergeCommits(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "seed.txt", "s")
	git(t, dir, "checkout", "-qb", "side")
	commit(t, dir, "side.txt", "s")
	git(t, dir, "checkout", "-q", "main")
	commit(t, dir, "main.txt", "m")
	git(t, dir, "merge", "--no-ff", "-qm", "merge side", "side")
	cs, err := CommitsIn(dir, base, git(t, dir, "rev-parse", "HEAD"))
	require.NoError(t, err)
	assert.Len(t, cs, 3)
	assert.Equal(t, "merge side", cs[2].Subject)
}
