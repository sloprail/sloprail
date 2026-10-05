package sessionstate

import (
	"database/sql"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

type registryFixture struct {
	live, gone, other string
}

// seed fills a store the way a long session leaves it: rows for a live repository and for folders
// that were removed since, observations of branches that exist and of branches that were deleted.
func seedRegistry(t *testing.T, db *sql.DB) registryFixture {
	t.Helper()
	live := t.TempDir()
	gitIn(t, live, "init", "-q", "-b", "main")
	gitIn(t, live, "commit", "-q", "--allow-empty", "-m", "base")
	gitIn(t, live, "branch", "kept")
	other := t.TempDir()
	gone := filepath.Join(t.TempDir(), "removed-worktree")
	f := registryFixture{live: live, gone: gone, other: other}

	exec := func(q string, args ...any) {
		_, err := db.Exec(q, args...)
		require.NoError(t, err, q)
	}
	for _, r := range [][]any{
		{"s", live, "root", live, ""},
		{"s", other, "ad-hoc", other, "a1"},
		{"s", gone, "ad-hoc", gone, "a2"},                           // gone, nothing tracked there
		{"s", gone + "-tracked", "ad-hoc", gone + "-tracked", "a3"}, // gone, a tracked range names it
	} {
		exec(`INSERT INTO session_folders (session_id, path, role, git_root, agent_id) VALUES (?, ?, ?, ?, ?)`, r...)
	}
	exec(`INSERT INTO session_agent_folders (session_id, agent_id, folder) VALUES ('s','a2',?), ('s','a1',?)`, gone, other)
	ref := func(folder, name, reason string) {
		exec(`INSERT INTO session_refs (session_id, folder, ref, first_tip, tip, untracked_reason, abandoned_tip) VALUES ('s', ?, ?, 'a', 'a', ?, ?)`,
			folder, name, reason, map[bool]string{true: "a", false: ""}[reason != ""])
	}
	ref(live, "kept", "")                                                       // tracked: stays
	ref(live, "kept-untracked", "the agent said so")                            // a decision: stays
	ref(live, "refs/heads/kept", "pruned: tracked at first sight, never moved") // branch exists: stays
	ref(live, "deleted-branch", "pruned: tracked at first sight, never moved")  // branch is gone: dropped
	ref(live, "refs/heads/deleted-too", "pruned: the branch is answered for by its home folder")
	ref(live, "0123456789abcdef0123456789abcdef01234567", "pruned: tracked at first sight, never moved") // a bare commit: stays
	ref(gone, "wt", "finished work: the worktree was removed")                                           // untracked, folder gone: dropped
	ref(gone+"-tracked", "wt", "")                                                                       // tracked, folder gone: stays, with its folder row
	ref(gone+"-tracked", "old", "pruned: x")                                                             // untracked, folder gone: dropped

	set := func(k string) {
		exec(`INSERT INTO meta (key, value) VALUES (?, 'x')`, k)
	}
	for _, k := range []string{
		"observed-tip:kept:" + live, "observed-tip:main:" + live, "observed-tip:(detached):" + live,
		"observed-tip:deleted-branch:" + live, "observed-tip:wt:" + gone, "foreign-cover:kept:" + live,
		"foreign-cover:deleted-branch:" + live, "foreign-cover:wt:" + gone,
		"observed-folder:" + live, "observed-folder:" + gone,
		"refs_at_start:" + live, "refs_snapshot:" + live, "ref_start:" + live, "folder_home:" + gone,
		"orphan_origin:x", "orphan_adopted:x", "retired_tips:x", "checks_imported:abc", "refs_schema", "cited_unknown",
		"baseline_commit", "verify-memo:aa", "cited_pending",
	} {
		set(k)
	}
	return f
}

func metaKeys(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT key FROM meta`)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		require.NoError(t, rows.Scan(&k))
		out[k] = true
	}
	return out
}

func refKeys(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT folder || '|' || ref FROM session_refs`)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		require.NoError(t, rows.Scan(&k))
		out[k] = true
	}
	return out
}

func folderPaths(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT path FROM session_folders`)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		require.NoError(t, rows.Scan(&k))
		out[k] = true
	}
	return out
}

func TestPruneGone_KeepsWhatCanStillBeAskedAboutAndDropsTheRest(t *testing.T) {
	s, err := open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer s.Close()
	f := seedRegistry(t, s.db)

	require.NoError(t, pruneGone(s.db, true))

	refs := refKeys(t, s.db)
	assert.True(t, refs[f.live+"|kept"], "a tracked range stays")
	assert.True(t, refs[f.live+"|kept-untracked"], "a decision the agent made stays")
	assert.True(t, refs[f.live+"|refs/heads/kept"], "housekeeping for a branch that still exists stays")
	assert.True(t, refs[f.live+"|0123456789abcdef0123456789abcdef01234567"], "a bare commit is not a branch that can be gone")
	assert.True(t, refs[f.gone+"-tracked|wt"], "a tracked range of a folder that is gone is still verified at its commit")
	assert.False(t, refs[f.live+"|deleted-branch"])
	assert.False(t, refs[f.live+"|refs/heads/deleted-too"])
	assert.False(t, refs[f.gone+"|wt"], "an untracked range of a gone folder decides nothing")
	assert.False(t, refs[f.gone+"-tracked|old"])

	folders := folderPaths(t, s.db)
	assert.True(t, folders[f.live] && folders[f.other], "live folders stay")
	assert.True(t, folders[f.gone+"-tracked"], "a gone folder a tracked range names stays")
	assert.False(t, folders[f.gone])
	var n int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM session_agent_folders`).Scan(&n))
	assert.Equal(t, 1, n, "only the live agent folder stays")

	keys := metaKeys(t, s.db)
	for _, k := range []string{"observed-tip:kept:" + f.live, "observed-tip:main:" + f.live, "observed-tip:(detached):" + f.live,
		"foreign-cover:kept:" + f.live, "observed-folder:" + f.live, "baseline_commit", "verify-memo:aa", "cited_pending"} {
		assert.True(t, keys[k], "%s is still asked about", k)
	}
	for _, k := range []string{"observed-tip:deleted-branch:" + f.live, "foreign-cover:deleted-branch:" + f.live,
		"observed-tip:wt:" + f.gone, "foreign-cover:wt:" + f.gone, "observed-folder:" + f.gone,
		"refs_at_start:" + f.live, "refs_snapshot:" + f.live, "ref_start:" + f.live, "folder_home:" + f.gone,
		"orphan_origin:x", "orphan_adopted:x", "retired_tips:x", "checks_imported:abc", "refs_schema", "cited_unknown"} {
		assert.False(t, keys[k], "%s is dropped", k)
	}
}

// At run time git is asked only once the registry is past its bounds; the cheap part (folders that
// are gone) always runs, and it runs at most once in a while.
func TestPruneGone_AtRunTimeAsksGitOnlyPastTheBoundsAndOnlyOnceInAWhile(t *testing.T) {
	s, err := open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer s.Close()
	f := seedRegistry(t, s.db)

	require.NoError(t, s.PruneGone())
	refs := refKeys(t, s.db)
	assert.False(t, refs[f.gone+"|wt"], "a gone folder is dropped at once")
	assert.True(t, refs[f.live+"|deleted-branch"], "which branches exist is not asked while the registry is small")

	_, err = s.db.Exec(`INSERT INTO session_refs (session_id, folder, ref, first_tip, tip, untracked_reason, abandoned_tip) VALUES ('s', ?, 'again', 'a', 'a', 'x', 'a')`, f.gone)
	require.NoError(t, err)
	require.NoError(t, s.PruneGone())
	assert.True(t, refKeys(t, s.db)[f.gone+"|again"], "it runs once in a while, not at every hook")

	// Past the bound it asks.
	for i := 0; i <= maxPrunedRows; i++ {
		_, err = s.db.Exec(`INSERT INTO session_refs (session_id, folder, ref, first_tip, tip, untracked_reason, abandoned_tip) VALUES ('s', ?, ?, 'a', 'a', 'pruned: x', 'a')`, f.live, fmt.Sprintf("gone-%d", i))
		require.NoError(t, err)
	}
	require.NoError(t, pruneGone(s.db, false))
	refs = refKeys(t, s.db)
	assert.False(t, refs[f.live+"|deleted-branch"])
	assert.False(t, refs[f.live+"|gone-7"])
	assert.True(t, refs[f.live+"|refs/heads/kept"])
}

func TestPruneGone_AVerifyMemoPastItsCapStartsAfresh(t *testing.T) {
	s, err := open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer s.Close()
	for i := 0; i < maxVerifyMemos; i++ {
		require.NoError(t, s.SetMeta(fmt.Sprintf("verify-memo:%05d", i), "P"))
	}
	require.NoError(t, pruneGone(s.db, false))
	keys, err := s.MetaKeys("verify-memo:")
	require.NoError(t, err)
	assert.Len(t, keys, maxVerifyMemos, "at the cap it stays")
	require.NoError(t, s.SetMeta("verify-memo:extra", "P"))
	require.NoError(t, pruneGone(s.db, false))
	keys, err = s.MetaKeys("verify-memo:")
	require.NoError(t, err)
	assert.Empty(t, keys)
}

// A store written before this pruning (user_version 9) is pruned by the engine on open, and what is
// still owed — tracked ranges, live folders, observations of live branches — stays.
func TestOpen_AStoreFromBeforePruningIsPrunedOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	files, err := migrationFiles()
	require.NoError(t, err)
	require.Equal(t, "010_prune_gone.sql", files[9])
	for _, f := range files[:9] {
		body, err := migrationFS.ReadFile("migrations/" + f)
		require.NoError(t, err)
		_, err = old.Exec(string(body))
		require.NoError(t, err, f)
	}
	_, err = old.Exec(`PRAGMA user_version = 9`)
	require.NoError(t, err)
	fx := seedRegistry(t, old)
	require.NoError(t, old.Close())

	s, err := Open(path)
	require.NoError(t, err)
	defer s.Close()
	rs, err := s.Ranges("s")
	require.NoError(t, err)
	var heads []string
	for _, r := range rs {
		heads = append(heads, filepath.Base(r.Folder)+"|"+r.Head)
	}
	assert.Contains(t, heads, filepath.Base(fx.live)+"|kept")
	assert.Contains(t, heads, filepath.Base(fx.gone+"-tracked")+"|wt")
	assert.NotContains(t, heads, filepath.Base(fx.gone)+"|wt")
	v, ok, err := s.Meta("observed-tip:kept:" + fx.live)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "x", v)
	_, ok, err = s.Meta("refs_snapshot:" + fx.live)
	require.NoError(t, err)
	assert.False(t, ok)
}
