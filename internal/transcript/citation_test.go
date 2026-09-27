package transcript

import (
	"testing"

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
