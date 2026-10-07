package checkrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
)

// A stored pass is about the content, so the citation requirement, which is no part of its key,
// is asked of every range that reaches it: `run` and `verify` alike. Identical content with no
// citation is refused (and the refusal is not stored over the pass); with one it is a hit and
// no check runs again.
func TestCitationGate_AStoredPassDoesNotWaiveTheCitationOfAnotherRange(t *testing.T) {
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

	// The same content, committed with no citation.
	runGit(t, f.repo, "checkout", "-q", "-b", "uncited", rule)
	require.NoError(t, os.MkdirAll(filepath.Join(f.repo, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "docs", "a.md"), []byte("clean"), 0o644))
	// (another file too, so the trees are not the cited range's: that is the squash-merge reuse)
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "other.txt"), []byte("not selected"), 0o644))
	runGit(t, f.repo, "add", "-A")
	runGit(t, f.repo, "commit", "-m", "doc again, no quote")

	r, refused = f.evaluateWith(t, session, "b2")
	require.True(t, refused, "the content was judged, the citation was not given")
	assert.Contains(t, r.Reason, "must cite the user's own words")
	got := f.verifyReasons(t)
	require.Len(t, got, 1, "verify asks the citation too")
	assert.Contains(t, got[0].Reason, "must cite the user's own words")
	assert.Equal(t, 1, f.runs(t), "no check ran again")

	// The same content with a citation: a hit, the check does not run, run and verify pass.
	runGit(t, f.repo, "checkout", "-q", "-b", "cited", rule)
	require.NoError(t, os.MkdirAll(filepath.Join(f.repo, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "docs", "a.md"), []byte("clean"), 0o644))
	runGit(t, f.repo, "add", "-A")
	runGit(t, f.repo, "commit", "-m", "doc, reworded", "-m", changeset.TrailerCitesUser+": the user said so")
	r, refused = f.evaluateWith(t, session, "b3")
	require.False(t, refused, r.Reason)
	assert.Equal(t, 1, f.runs(t), "the stored pass is reused: citations are not in the key")
	assert.Empty(t, f.verifyReasons(t))

	// A reworded quote is no new key either.
	runGit(t, f.repo, "checkout", "-q", "-b", "reworded", rule)
	require.NoError(t, os.MkdirAll(filepath.Join(f.repo, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "docs", "a.md"), []byte("clean"), 0o644))
	runGit(t, f.repo, "add", "-A")
	runGit(t, f.repo, "commit", "-m", "doc", "-m", changeset.TrailerCitesUser+": the user said")
	// "the user said" is a substring of the session's message, so it resolves: still a hit.
	r, refused = f.evaluateWith(t, session, "b4")
	require.False(t, refused, r.Reason)
	assert.Equal(t, 1, f.runs(t))
}
