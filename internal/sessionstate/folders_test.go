package sessionstate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFolder_AbsentIsAnAnswerNotAnError(t *testing.T) {
	s := openTestStore(t)
	_, found, err := s.Folder("sess", "/repo")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestRegisterFolder_RoundTripsEveryField(t *testing.T) {
	s := openTestStore(t)
	want := Folder{
		SessionID: "sess", Path: "/repo/.claude/worktrees/agent-1", Role: FolderSubagentWorktree,
		GitRoot: "/repo/.claude/worktrees/agent-1", RepoID: "rootcommit", Branch: "worktree-agent-1",
		BaseRef: "abc123", HeadRef: "abc123", AgentID: "agent-1",
	}
	wrote, err := s.RegisterFolder(want)
	require.NoError(t, err)
	assert.True(t, wrote)

	got, found, err := s.Folder("sess", want.Path)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, want, got)
}

func TestRegisterFolder_BaseRefNeverMoves(t *testing.T) {
	// The start is where work in the folder began. A later call knows only where
	// the folder is now, and must not replace it.
	s := openTestStore(t)
	_, err := s.RegisterFolder(Folder{SessionID: "sess", Path: "/wt", Role: FolderSubagentWorktree, BaseRef: "first"})
	require.NoError(t, err)

	wrote, err := s.RegisterFolder(Folder{SessionID: "sess", Path: "/wt", Role: FolderSubagentWorktree, BaseRef: "later"})
	require.NoError(t, err)
	assert.False(t, wrote)

	got, _, err := s.Folder("sess", "/wt")
	require.NoError(t, err)
	assert.Equal(t, "first", got.BaseRef)
}

func TestRegisterFolder_KeyedBySessionAndPath(t *testing.T) {
	// One path under two sessions is two folders with two starts; two paths under
	// one session are two folders too — a worktree's start is not its parent's.
	s := openTestStore(t)
	for _, f := range []Folder{
		{SessionID: "a", Path: "/repo", Role: FolderRoot, BaseRef: "A"},
		{SessionID: "a", Path: "/repo/wt", Role: FolderSubagentWorktree, BaseRef: "C"},
		{SessionID: "b", Path: "/repo", Role: FolderRoot, BaseRef: "other"},
	} {
		_, err := s.RegisterFolder(f)
		require.NoError(t, err)
	}

	root, _, _ := s.Folder("a", "/repo")
	wt, _, _ := s.Folder("a", "/repo/wt")
	other, _, _ := s.Folder("b", "/repo")
	assert.Equal(t, "A", root.BaseRef)
	assert.Equal(t, "C", wt.BaseRef)
	assert.Equal(t, "other", other.BaseRef)
}

func TestFolders_ListsTheSessionsOwnRootFirst(t *testing.T) {
	s := openTestStore(t)
	for _, f := range []Folder{
		{SessionID: "a", Path: "/aaa/wt", Role: FolderSubagentWorktree},
		{SessionID: "a", Path: "/zzz", Role: FolderRoot},
		{SessionID: "b", Path: "/elsewhere", Role: FolderRoot},
	} {
		_, err := s.RegisterFolder(f)
		require.NoError(t, err)
	}

	got, err := s.Folders("a")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "/zzz", got[0].Path, "the root comes first whatever its path sorts as")
	assert.Equal(t, "/aaa/wt", got[1].Path)
}

func TestRegisterFolder_NeedsASessionAndAPath(t *testing.T) {
	s := openTestStore(t)
	_, err := s.RegisterFolder(Folder{Path: "/repo"})
	assert.Error(t, err)
	_, err = s.RegisterFolder(Folder{SessionID: "a"})
	assert.Error(t, err)
}

func TestSetFolderHead_UpdatesOnlyAnExistingFolder(t *testing.T) {
	s := openTestStore(t)
	_, err := s.RegisterFolder(Folder{SessionID: "a", Path: "/repo", Role: FolderRoot, BaseRef: "A", HeadRef: "A"})
	require.NoError(t, err)

	require.NoError(t, s.SetFolderHead("a", "/repo", "B"))
	require.NoError(t, s.SetFolderHead("a", "/not-registered", "B"))

	got, _, _ := s.Folder("a", "/repo")
	assert.Equal(t, "B", got.HeadRef)
	assert.Equal(t, "A", got.BaseRef, "the head moves, the start does not")
	_, found, _ := s.Folder("a", "/not-registered")
	assert.False(t, found, "updating a head must not register a folder")
}
