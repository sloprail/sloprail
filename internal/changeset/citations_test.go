package changeset

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/transcript"
)

// fakeResolver resolves the quotes it is told about and refuses the rest, the
// way the transcript does: by pool.
func fakeResolver(known map[string]transcript.SourceType) (Resolver, *[]transcript.CitationRequest) {
	var asked []transcript.CitationRequest
	return func(req transcript.CitationRequest) (transcript.Citation, error) {
		asked = append(asked, req)
		pool, ok := known[req.Quote]
		if !ok || req.SourceTypes[0] != pool {
			return transcript.Citation{}, &transcript.ResolutionError{Msg: "does not resolve"}
		}
		return transcript.Citation{Quote: req.Quote, SourceTypes: []transcript.SourceType{pool}, Path: "/t.jsonl", Line: 7, Message: "msg " + req.Quote}, nil
	}, &asked
}

func commitWith(sha string, trailers map[string][]string) Commit {
	return Commit{SHA: sha, Subject: "s", Trailers: trailers}
}

func TestResolveCitations_UserAndToolTrailersLandInTheEventCitationShape(t *testing.T) {
	resolve, asked := fakeResolver(map[string]transcript.SourceType{
		"split the runners": transcript.SourceUser,
		"make test passed":  transcript.SourceToolResult,
	})
	cites, unresolved := ResolveCitations([]Commit{
		commitWith("c1", map[string][]string{TrailerCitesUser: {"split the runners"}, TrailerCitesTool: {"make test passed"}}),
	}, resolve)

	assert.Empty(t, unresolved)
	require.Len(t, cites, 2)
	assert.Equal(t, transcript.Citation{Quote: "split the runners", SourceTypes: []transcript.SourceType{transcript.SourceUser}, Path: "/t.jsonl", Line: 7, Message: "msg split the runners"}, cites[0])
	assert.Equal(t, []transcript.SourceType{transcript.SourceToolResult}, cites[1].SourceTypes)
	assert.Equal(t, []transcript.SourceType{transcript.SourceUser}, (*asked)[0].SourceTypes, "Cites-User asks the user pool only")
	assert.Equal(t, []transcript.SourceType{transcript.SourceToolResult}, (*asked)[1].SourceTypes, "Cites-Tool asks the tool pool only")
}

func TestResolveCitations_AnUnresolvableQuoteIsNotACitationAndIsReported(t *testing.T) {
	resolve, _ := fakeResolver(map[string]transcript.SourceType{})
	cites, unresolved := ResolveCitations([]Commit{
		commitWith("abcdef0123456789", map[string][]string{TrailerCitesUser: {"nobody said this"}}),
	}, resolve)
	assert.Empty(t, cites)
	require.Len(t, unresolved, 1)
	assert.Equal(t, "nobody said this", unresolved[0].Quote)
	assert.Equal(t, TrailerCitesUser, unresolved[0].Trailer)
	assert.Contains(t, unresolved[0].String(), "abcdef012345")
}

func TestResolveCitations_ATrailerCannotBorrowAnotherPool(t *testing.T) {
	// The words are in the tool output, and the commit claims the user said them.
	resolve, _ := fakeResolver(map[string]transcript.SourceType{"green": transcript.SourceToolResult})
	cites, unresolved := ResolveCitations([]Commit{
		commitWith("c1", map[string][]string{TrailerCitesUser: {"green"}}),
	}, resolve)
	assert.Empty(t, cites)
	assert.Len(t, unresolved, 1)
}

func TestResolveCitations_AResolverThatAnswersFromTheWrongPoolIsNotTrusted(t *testing.T) {
	wrong := func(req transcript.CitationRequest) (transcript.Citation, error) {
		return transcript.Citation{Quote: req.Quote, SourceTypes: []transcript.SourceType{transcript.SourceToolResult}}, nil
	}
	cites, unresolved := ResolveCitations([]Commit{commitWith("c1", map[string][]string{TrailerCitesUser: {"x"}})}, wrong)
	assert.Empty(t, cites)
	assert.Len(t, unresolved, 1)
}

func TestResolveCitations_AccumulateAcrossCommitsInOrderAndDedupe(t *testing.T) {
	resolve, asked := fakeResolver(map[string]transcript.SourceType{"a": transcript.SourceUser, "b": transcript.SourceUser})
	cites, _ := ResolveCitations([]Commit{
		commitWith("c1", map[string][]string{TrailerCitesUser: {"a"}}),
		commitWith("c2", map[string][]string{TrailerCitesUser: {"b", "a"}}),
	}, resolve)
	require.Len(t, cites, 2)
	assert.Equal(t, "a", cites[0].Quote)
	assert.Equal(t, "b", cites[1].Quote)
	assert.Len(t, *asked, 2, "a quote cited twice is resolved once")
}

func TestResolveCitations_OtherTrailersAreNotCitations(t *testing.T) {
	resolve, asked := fakeResolver(nil)
	cites, unresolved := ResolveCitations([]Commit{commitWith("c1", map[string][]string{"Sloprail-Refactor": {"move-only"}, "Signed-off-by": {"x"}})}, resolve)
	assert.Empty(t, cites)
	assert.Empty(t, unresolved)
	assert.Empty(t, *asked)
}

func TestResolveCitations_NoCommitsIsAnEmptyListNotNil(t *testing.T) {
	cites, unresolved := ResolveCitations(nil, nil)
	assert.NotNil(t, cites, "JSON renders [] not null, so `.changeset.citations[]` is safe")
	assert.Empty(t, unresolved)
}

func TestResolveCitations_AnEmptyQuoteIsNotACitation(t *testing.T) {
	resolve := func(transcript.CitationRequest) (transcript.Citation, error) {
		return transcript.Citation{}, errors.New("an empty quote grounds nothing")
	}
	cites, unresolved := ResolveCitations([]Commit{commitWith("c1", map[string][]string{TrailerCitesUser: {""}})}, resolve)
	assert.Empty(t, cites)
	assert.Len(t, unresolved, 1)
}
