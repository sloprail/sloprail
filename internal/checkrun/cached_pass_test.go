package checkrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
)

// A cached PASS for a subject's content means nothing runs for it: no citation requirement, no
// check. A pass says the content was fine (and so the citation was right), so the same content
// reached over other or no citations stays passing, in `run` and `verify` alike. Changed
// content is a miss, and a miss runs everything: its citation requirement refuses it.
func TestCachedPass_NothingRunsForTheSameContentWhateverTheCitations(t *testing.T) {
	f := citedFixture(t)
	session := filepath.Join(t.TempDir(), "session.jsonl")
	require.NoError(t, os.WriteFile(session, []byte(
		`{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"s1","cwd":"/x","message":{"role":"user","content":"the user said so"}}`+"\n"), 0o644))
	rule := runGit(t, f.repo, "rev-parse", "HEAD~1")
	f.base = rule

	r, refused := f.evaluateWith(t, session, "b1")
	require.False(t, refused, r.Reason)
	require.Equal(t, 1, f.runs(t), "the check ran once")
	assert.Empty(t, f.verifyReasons(t), "the cited range is green")

	// The same content, committed with no citation (and another file, so the trees differ).
	runGit(t, f.repo, "checkout", "-q", "-b", "uncited", rule)
	require.NoError(t, os.MkdirAll(filepath.Join(f.repo, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "docs", "a.md"), []byte("clean"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "other.txt"), []byte("not selected"), 0o644))
	runGit(t, f.repo, "add", "-A")
	runGit(t, f.repo, "commit", "-m", "doc again, no quote")
	r, refused = f.evaluateWith(t, session, "b2")
	require.False(t, refused, "the content passed: "+r.Reason)
	assert.Equal(t, 1, f.runs(t), "nothing ran again")
	assert.Empty(t, f.verifyReasons(t), "verify reads the same pass")

	// Other content with no citation is a miss, and a miss runs everything.
	runGit(t, f.repo, "checkout", "-q", "-b", "changed", rule)
	require.NoError(t, os.MkdirAll(filepath.Join(f.repo, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "docs", "a.md"), []byte("clean, edited"), 0o644))
	runGit(t, f.repo, "add", "-A")
	runGit(t, f.repo, "commit", "-m", "edit, no quote", "-m", changeset.TrailerCitesUser+": ")
	r, refused = f.evaluateWith(t, session, "b3")
	require.True(t, refused)
	assert.Contains(t, r.Reason, "must cite the user's own words")
	got := f.verifyReasons(t)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Reason, "must cite the user's own words", "the refusal is stored under the content key, and verify says why")
}

// A citation refusal is stored under the content key like any other refusal, so verify can say
// why; the next run judges the content again, since only a pass is a hit.
func TestCachedPass_ACitationRefusalIsStoredAndJudgedAgain(t *testing.T) {
	f := citedFixture(t)
	record := filepath.Join(t.TempDir(), "s.jsonl")
	require.NoError(t, os.WriteFile(record, []byte(""), 0o644))
	r, refused := f.evaluateWith(t, record, "b1")
	require.True(t, refused)
	assert.Len(t, guardRows(t, f.results, f.guard), 1, "stored under the content key")
	got := f.verifyReasons(t)
	require.Len(t, got, 1)
	assert.Equal(t, r.Reason, got[0].Reason, "verify says why")

	// The quote now resolves in the session: the same content is judged again and passes.
	require.NoError(t, os.WriteFile(record, []byte(
		`{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"s1","cwd":"/x","message":{"role":"user","content":"the user said so"}}`+"\n"), 0o644))
	r, refused = f.evaluateWith(t, record, "b2")
	require.False(t, refused, r.Reason)
	assert.Empty(t, f.verifyReasons(t))
}
