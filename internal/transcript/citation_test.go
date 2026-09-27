package transcript

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func userReq(q string) CitationRequest {
	return CitationRequest{Quote: q, SourceTypes: []SourceType{SourceUser}}
}

func TestResolveCitationSinglePool(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session", userMsg("u1", "always use snake_case for filenames"))

	got, err := ResolveCitation(path, userReq("always use snake_case"))
	require.NoError(t, err)
	assert.Equal(t, Citation{
		Quote:       "always use snake_case",
		SourceTypes: []SourceType{SourceUser},
		Path:        path,
		Line:        1,
		Message:     "always use snake_case for filenames",
	}, got)
}

func TestResolveCitationRecordsOnlyThePoolItLandedIn(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "run the suite"),
		record("a1", "u1"),
		toolResultMsg("u2", "a1", "SUITEMARKER passed"),
	)

	got, err := ResolveCitation(path, CitationRequest{
		Quote:       "SUITEMARKER",
		SourceTypes: []SourceType{SourceUser, SourceToolResult},
	})
	require.NoError(t, err)
	assert.Equal(t, []SourceType{SourceToolResult}, got.SourceTypes)
	assert.Equal(t, 3, got.Line)
	assert.Equal(t, "SUITEMARKER passed", got.Message, "the whole tool output, not only the quote")
}

func TestResolveCitationCapsAHugeMessage(t *testing.T) {
	p := newProject(t)
	huge := "HUGEMARKER " + strings.Repeat("é", maxCitedMessage)
	path := p.write("a-session", userMsg("u1", huge))

	got, err := ResolveCitation(path, userReq("HUGEMARKER"))
	require.NoError(t, err)
	assert.LessOrEqual(t, len(got.Message), maxCitedMessage+len("\n[... truncated]"))
	assert.True(t, strings.HasSuffix(got.Message, "[... truncated]"))
	assert.True(t, utf8.ValidString(got.Message), "the cap never splits a character")
}

func TestResolveCitationsInOrder(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "the ask is FIRSTMARKER"),
		record("a1", "u1"),
		toolResultMsg("u2", "a1", "SECONDMARKER passed"),
	)

	got, err := ResolveCitations(path, []CitationRequest{
		userReq("FIRSTMARKER"),
		{Quote: "SECONDMARKER", SourceTypes: []SourceType{SourceToolResult}},
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 1, got[0].Line)
	assert.Equal(t, 3, got[1].Line)
}

func TestResolveCitationsFailsClosedOnAnyUnresolved(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session", userMsg("u1", "the ask is FIRSTMARKER"))

	got, err := ResolveCitations(path, []CitationRequest{userReq("FIRSTMARKER"), userReq("NEVERSAID")})
	require.Error(t, err)
	assert.Nil(t, got, "a partial citation list must never be returned")
}

func TestResolveCitationRefusals(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "please DUPMARKER this"),
		record("a1", "u1"),
		userMsg("u2", "no really, DUPMARKER this, ONLYUSER"),
	)

	for name, req := range map[string]CitationRequest{
		"ambiguous":   userReq("DUPMARKER"),
		"wrong pool":  {Quote: "ONLYUSER", SourceTypes: []SourceType{SourceToolResult}},
		"empty quote": userReq(""),
		"no pool":     {Quote: "ONLYUSER"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveCitation(path, req)
			require.Error(t, err)
		})
	}
}

func TestResolveCitationOfAnAnswerCarriesTheQuestion(t *testing.T) {
	p := newProject(t)
	envelope := `The user answered: "which approach for the auth rewrite?"="go with the second option please". Read the answers carefully.`
	path := p.write("a-session",
		userMsg("u1", "here is the task"),
		record("a1", "u1"),
		`{"type":"user","uuid":"u2","parentUuid":"a1","isSidechain":false,"message":{"role":"user","content":[`+
			`{"type":"tool_result","tool_use_id":"t1","content":`+jsonQuote(envelope)+`}]}}`,
	)

	got, err := ResolveCitation(path, userReq("second option"))
	require.NoError(t, err)
	assert.Equal(t, 3, got.Line)
	assert.Contains(t, got.Message, "which approach for the auth rewrite?", "the question comes with the answer")
	assert.Contains(t, got.Message, "go with the second option please")
}
