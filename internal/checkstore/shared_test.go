package checkstore

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func passRun(t *testing.T, s Store, head, fp string) {
	t.Helper()
	r := run(head)
	id, err := s.RecordRun(r)
	require.NoError(t, err)
	_, err = s.RecordCheck(id, CheckRecord{Subject: "changeset", Kind: "check[0]:judge:x", Status: StatusPass, Fingerprint: fp})
	require.NoError(t, err)
	require.NoError(t, s.FinishRun(id))
}

func TestFamilyStores_ShareOneFileButReadOnlyTheirOwnRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos", "r1", "checks.db")
	a, err := OpenFamily(path, "sess-a")
	require.NoError(t, err)
	defer a.Close()
	b, err := OpenFamily(path, "sess-b")
	require.NoError(t, err)
	defer b.Close()

	passRun(t, a, "headA", "fpA")
	heads, err := b.PassedHeads(rule)
	require.NoError(t, err)
	assert.Empty(t, heads, "another family's pass is not this family's watermark")
	heads, err = a.PassedHeads(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"headA"}, heads)

	rows, err := b.Query("select count(*) as n from check_runs")
	require.NoError(t, err)
	assert.EqualValues(t, 0, rows[0]["n"])
	cols, err := a.Query("select name from pragma_table_info('check_runs') order by cid")
	require.NoError(t, err)
	for _, c := range cols {
		assert.NotContains(t, []any{"family", "folder"}, c["name"], "the shared columns are not part of what a reader sees")
	}

	ro, err := OpenFamilyReadOnly(path, "sess-a")
	require.NoError(t, err)
	defer ro.Close()
	heads, err = ro.PassedHeads(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"headA"}, heads)
	_, err = ro.RecordRun(run("x"))
	assert.Error(t, err, "a reader cannot write")
}

func TestFamilyStores_ReuseAPassOnExactInputOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checks.db")
	a, err := OpenFamily(path, "sess-a")
	require.NoError(t, err)
	defer a.Close()
	b, err := OpenFamily(path, "sess-b")
	require.NoError(t, err)
	defer b.Close()
	passRun(t, a, "h1", "fp-same")

	got, ok, err := b.CachedCheck("changeset", "check[0]:judge:x", "fp-same")
	require.NoError(t, err)
	assert.True(t, ok, "the same input, passed by another session, stands")
	assert.Equal(t, StatusPass, got.Status)
	_, ok, err = b.CachedCheck("changeset", "check[0]:judge:x", "fp-other")
	require.NoError(t, err)
	assert.False(t, ok, "different input is never reused")

	id, err := a.RecordRun(run("h2"))
	require.NoError(t, err)
	_, err = a.RecordCheck(id, CheckRecord{Subject: "changeset", Kind: "check[0]:judge:x", Status: StatusFail, Fingerprint: "fp-failed"})
	require.NoError(t, err)
	_, ok, err = b.CachedCheck("changeset", "check[0]:judge:x", "fp-failed")
	require.NoError(t, err)
	assert.False(t, ok, "a refusal stays with the session that was refused")
}

func TestFamilyStores_TheTablesKeepTheirRowidForARuleThatOrdersByIt(t *testing.T) {
	a, err := OpenFamily(filepath.Join(t.TempDir(), "checks.db"), "sess-a")
	require.NoError(t, err)
	defer a.Close()
	passRun(t, a, "h1", "fp1")
	passRun(t, a, "h2", "fp2")
	// The shape of the merge gate's own query: a later run of the same rule, by rowid.
	rows, err := a.Query(`select r1.head_ref from check_runs r1 where exists
		(select 1 from check_runs r2 where r2.check_id = r1.check_id and r2.rowid > r1.rowid)`)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "h1", rows[0]["head_ref"])
}

func TestSiblingRunRefs_AreOtherFamiliesOfTheSameFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checks.db")
	a, err := OpenFamily(path, "sess-a")
	require.NoError(t, err)
	defer a.Close()
	b, err := OpenFamily(path, "sess-b")
	require.NoError(t, err)
	defer b.Close()
	r := run("hA")
	r.Folder = "/wt"
	id, err := a.RecordRun(r)
	require.NoError(t, err)
	_, err = a.RecordCheck(id, CheckRecord{Subject: "changeset", Kind: "k", Status: StatusFail})
	require.NoError(t, err)
	require.NoError(t, a.FinishRun(id))

	refs, err := b.(SiblingRefs).SiblingRunRefs(rule, "/wt")
	require.NoError(t, err)
	require.Len(t, refs.Failed, 1)
	assert.Equal(t, "hA", refs.Failed[0].Head)
	refs, err = b.(SiblingRefs).SiblingRunRefs(rule, "/elsewhere")
	require.NoError(t, err)
	assert.Empty(t, refs.Failed)
	refs, err = a.(SiblingRefs).SiblingRunRefs(rule, "/wt")
	require.NoError(t, err)
	assert.Empty(t, refs.Failed, "a family is not its own sibling")
}

func TestImportLegacy_IsIdempotentAndReadsAFileAgainWhenItChanges(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "sessions", "ws", "s1", "checks.db")
	old, err := Open(oldPath)
	require.NoError(t, err)
	passRun(t, old, "h-old", "fp1")
	require.NoError(t, old.Close())

	dst, err := OpenFamily(filepath.Join(dir, "repos", "r", "checks.db"), "s1")
	require.NoError(t, err)
	defer dst.Close()
	src := []Legacy{{Path: oldPath, Family: "s1", Folder: "/wt"}}
	require.NoError(t, ImportLegacy(dst, src))
	require.NoError(t, ImportLegacy(dst, src))
	heads, err := dst.PassedHeads(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"h-old"}, heads)

	old, err = Open(oldPath)
	require.NoError(t, err)
	passRun(t, old, "h-new", "fp2")
	require.NoError(t, old.Close())
	require.NoError(t, ImportLegacy(dst, src))
	heads, err = dst.PassedHeads(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"h-new", "h-old"}, heads)
}
