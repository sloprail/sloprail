package transcript

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func age(t *testing.T, path string, ago time.Duration) {
	t.Helper()
	when := time.Now().Add(-ago)
	require.NoError(t, os.Chtimes(path, when, when))
}

func TestAcrossSessions_CurrentSessionAnswersFirst(t *testing.T) {
	p := newProject(t)
	cur := p.write("cur", userMsg("u1", "split the oversized runner files"))
	old := p.write("old", userMsg("u9", "split the oversized runner files"))
	age(t, old, time.Hour)

	got, err := ResolveCitationAcrossSessions(cur, p.dir, userReq("oversized runner"))
	require.NoError(t, err)
	assert.Equal(t, cur, got.Path, "the current session is searched before any other, and stops the search")
}

func TestAcrossSessions_FallsBackToAnEarlierSession(t *testing.T) {
	p := newProject(t)
	cur := p.write("cur", userMsg("u1", "unrelated"))
	old := p.write("old", userMsg("u9", "keep the original transcript_path"))

	got, err := ResolveCitationAcrossSessions(cur, p.dir, userReq("original transcript_path"))
	require.NoError(t, err)
	assert.Equal(t, old, got.Path)
	assert.Equal(t, 1, got.Line)
}

func TestAcrossSessions_OtherSessionsNewestFirst(t *testing.T) {
	p := newProject(t)
	cur := p.write("cur", userMsg("u1", "unrelated"))
	older := p.write("a-older", userMsg("u1", "the quote lives here"))
	newer := p.write("z-newer", userMsg("u2", "the quote lives here"))
	age(t, older, 2*time.Hour)
	age(t, newer, time.Hour)

	got, err := ResolveCitationAcrossSessions(cur, p.dir, userReq("quote lives here"))
	require.NoError(t, err)
	assert.Equal(t, newer, got.Path, "newest first, whatever the file names sort to")
}

func TestAcrossSessions_TheFirstSessionContainingTheQuoteMustMatchOnce(t *testing.T) {
	p := newProject(t)
	cur := p.write("cur", userMsg("u1", "do the thing"), userMsg("u2", "do the thing again"))
	old := p.write("old", userMsg("u9", "do the thing"))
	age(t, old, time.Hour)

	_, err := ResolveCitationAcrossSessions(cur, p.dir, userReq("do the thing"))
	var res *ResolutionError
	require.ErrorAs(t, err, &res)
	assert.Contains(t, res.Msg, "ambiguous", "an ambiguity is not resolved by looking further back")
}

// sr:proves citations/user-pool-is-the-persons-own-words
func TestAcrossSessions_ModelTextIsNeverCitable(t *testing.T) {
	p := newProject(t)
	cur := p.write("cur", userMsg("u1", "start"), assistantText("a1", "u1", "MODELWORDS were said by the model"))
	_, err := ResolveCitationAcrossSessions(cur, p.dir, userReq("MODELWORDS"))
	var res *ResolutionError
	require.ErrorAs(t, err, &res)
}

// sr:proves citations/pool-is-not-borrowed
func TestAcrossSessions_ToolPoolIsSeparateFromTheUserPool(t *testing.T) {
	p := newProject(t)
	cur := p.write("cur",
		userMsg("u1", "run it"),
		toolUseMsg("a1", "u1", "Bash", "make test"),
		toolResultMsg("u2", "a1", "GREENRUN ok"))

	_, err := ResolveCitationAcrossSessions(cur, p.dir, userReq("GREENRUN"))
	require.Error(t, err, "a tool output is not the user's words")

	got, err := ResolveCitationAcrossSessions(cur, p.dir, CitationRequest{Quote: "GREENRUN", SourceTypes: []SourceType{SourceToolResult}})
	require.NoError(t, err)
	assert.Equal(t, []SourceType{SourceToolResult}, got.SourceTypes)
}

func TestAcrossSessions_NotFoundAnywhereIsAResolutionError(t *testing.T) {
	p := newProject(t)
	cur := p.write("cur", userMsg("u1", "hello"))
	_, err := ResolveCitationAcrossSessions(cur, p.dir, userReq("nobody said this"))
	var res *ResolutionError
	require.ErrorAs(t, err, &res)
}

// sr:proves citations/unresolved-trailers-are-reported-not-dropped
func TestAcrossSessions_AnUnreadableTranscriptDoesNotStopTheSearchButIsReported(t *testing.T) {
	p := newProject(t)
	cur := p.write("cur", userMsg("u1", "hello"))
	good := p.write("good", userMsg("u2", "the real quote"))
	bad := p.write("bad", userMsg("u3", "x"))
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	require.NoError(t, os.Chmod(bad, 0))
	t.Cleanup(func() { os.Chmod(bad, 0o644) })
	age(t, good, time.Hour)
	age(t, bad, time.Minute) // newer, so it is read first

	got, err := ResolveCitationAcrossSessions(cur, p.dir, userReq("real quote"))
	require.NoError(t, err)
	assert.Equal(t, good, got.Path)

	_, err = ResolveCitationAcrossSessions(cur, p.dir, userReq("absent"))
	var res *ResolutionError
	require.ErrorAs(t, err, &res)
	assert.Contains(t, res.Msg, "bad.jsonl", "what could not be read is named, so a miss is not a mystery")
}

func TestAcrossSessions_NoProjectDirSearchesTheCurrentSessionOnly(t *testing.T) {
	p := newProject(t)
	cur := p.write("cur", userMsg("u1", "only here"))
	got, err := ResolveCitationAcrossSessions(cur, "", userReq("only here"))
	require.NoError(t, err)
	assert.Equal(t, cur, got.Path)
}

func TestAcrossSessions_MissingProjectDirIsNotAnError(t *testing.T) {
	p := newProject(t)
	cur := p.write("cur", userMsg("u1", "only here"))
	_, err := ResolveCitationAcrossSessions(cur, p.dir+"/absent", userReq("only here"))
	require.NoError(t, err)
}

func TestAcrossSessions_EmptyQuoteGroundsNothing(t *testing.T) {
	_, err := ResolveCitationAcrossSessions("", "", userReq(""))
	assert.Error(t, err)
}
