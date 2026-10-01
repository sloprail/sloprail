package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

func TestRepoRelative_InsideTheRepoIsRelativeOutsideIsEmpty(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, ".sloprail", "file-guard", "size")
	require.NoError(t, os.MkdirAll(inside, 0o755))
	outside := t.TempDir() // a plugin cache, elsewhere on disk

	assert.Equal(t, ".sloprail/file-guard/size", repoRelative(root, inside))
	assert.Equal(t, "", repoRelative(root, outside), "a rule outside the repo has no folder for a floor")
	assert.Equal(t, "", repoRelative(root, root), "the root itself is not a rule folder")
}

func fileGuards(names ...string) declaration.Loaded {
	var l declaration.Loaded
	for _, n := range names {
		l.FileGuards = append(l.FileGuards, declaration.FileGuard{Name: n})
	}
	return l
}

func TestFindFileGuard_ByFolderNameOrQualifiedName(t *testing.T) {
	loaded := fileGuards("size", "docs")
	g, err := findFileGuard(loaded, "docs")
	require.NoError(t, err)
	assert.Equal(t, "docs", g.Name)
	g, err = findFileGuard(loaded, g.Qualified())
	require.NoError(t, err)
	assert.Equal(t, "docs", g.Name)
}

func TestFindFileGuard_UnknownNamesWhatIsLoaded(t *testing.T) {
	_, err := findFileGuard(fileGuards("size", "docs"), "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no file-guard "nope" is loaded`)
	assert.Contains(t, err.Error(), "file-guard/size")
	assert.Contains(t, err.Error(), "file-guard/docs")
}

func TestFindFileGuard_AmbiguityIsAnErrorNotAFirstWins(t *testing.T) {
	loaded := fileGuards("size")
	loaded.FileGuards = append(loaded.FileGuards, declaration.FileGuard{Name: "size", Origin: declaration.Origin{Plugin: "acme", Root: "/plugins/acme"}})
	_, err := findFileGuard(loaded, "size")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "names 2 file-guards")

	g, err := findFileGuard(loaded, loaded.FileGuards[1].Qualified())
	require.NoError(t, err)
	assert.True(t, g.Origin.FromPlugin(), "the qualified name picks the plugin's")
}

func TestContextsOf_WithoutAStoreEveryDeclaredContextIsInactive(t *testing.T) {
	got := contextsOf(discard(), nil, []declaration.Context{{Name: "goal"}, {Name: "research"}})
	require.Len(t, got, 2)
	assert.False(t, got["goal"].Active)
	assert.NotNil(t, got["goal"].Payload, "a match reading context[...] never meets nil")
}

func TestOpenChangesetSession_ReadsWhatExistsAndCreatesNothing(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	repo := initRepo(t)
	record := userRecord(t, "hello")
	p := HookPayload{Cwd: repo, TranscriptPath: record}

	// A session that has recorded nothing yet has no state and no results, and
	// looking must not make either.
	sess, err := openChangesetSession(&p)
	require.NoError(t, err)
	defer sess.close()
	assert.Equal(t, record, sess.record)
	assert.Nil(t, sess.state)
	assert.Nil(t, sess.checks)
	id, err := stableID(p)
	require.NoError(t, err)
	statePath, err := sessionDBPath(repo, id)
	require.NoError(t, err)
	_, statErr := os.Stat(statePath)
	assert.True(t, os.IsNotExist(statErr), "showing a changeset records nothing")

	// Once the session has both, both are opened.
	st, err := sessionstate.Open(statePath)
	require.NoError(t, err)
	st.Close()
	checksPath, err := sessionpath.ChecksDB(repo, id)
	require.NoError(t, err)
	cs, err := checkstore.Open(checksPath)
	require.NoError(t, err)
	cs.Close()

	p2 := HookPayload{Cwd: repo, TranscriptPath: record}
	sess2, err := openChangesetSession(&p2)
	require.NoError(t, err)
	defer sess2.close()
	assert.NotNil(t, sess2.state)
	assert.NotNil(t, sess2.checks)
	_, err = sess2.checks.PassedHeads("file-guard/x")
	assert.NoError(t, err, "a readable, read-only results store")
}
