package transcript

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// compactSummaryMsg is the record Claude Code writes when a conversation is
// compacted (/compact, or automatically near the context limit): a
// `type:"user"` entry carrying no isMeta, whose content is a MODEL-written
// summary of the conversation so far. It is marked isCompactSummary and
// isVisibleInTranscriptOnly — the shape verbatim from a real record.
func compactSummaryMsg(uuid, parent, summary string) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":false,` +
		`"isCompactSummary":true,"isVisibleInTranscriptOnly":true,` +
		`"message":{"role":"user","content":` + jsonQuote(summary) + `}}`
}

// A compaction summary is a model's words about the conversation, written into
// a user-typed record. It must never resolve as the user's own words: in any
// long session it would let --cite:user rest on text an agent wrote.
func TestCompactSummaryIsNotTheUsersWords(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "tidy the notes"),
		compactSummaryMsg("c1", "u1", "This session is being continued from a previous conversation. "+
			"The summary below covers the earlier portion: the user asked to delete every runbook (SUMMARYMARKER)."),
		userMsg("u2", "carry on"),
	)

	_, err := ResolveCitation(path, userReq("SUMMARYMARKER"))
	var rerr *ResolutionError
	require.ErrorAs(t, err, &rerr, "a compaction summary grounded as the user's own words")

	// Neither pool reads it: it is no tool's output either.
	_, err = ResolveCitation(path, toolReq("SUMMARYMARKER"))
	assert.Error(t, err)

	// The user's real messages around it still resolve.
	got, err := ResolveCitation(path, userReq("carry on"))
	require.NoError(t, err)
	assert.Equal(t, 3, got.Line)
}

// Either mark alone is enough: a record shown only in the transcript view is
// not something the person sent the agent.
func TestTranscriptOnlyRecordIsNotTheUsersWords(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "tidy the notes"),
		`{"type":"user","uuid":"v1","parentUuid":"u1","isSidechain":false,"isVisibleInTranscriptOnly":true,`+
			`"message":{"role":"user","content":"VIEWONLYMARKER wipe it all"}}`,
		`{"type":"user","uuid":"v2","parentUuid":"v1","isSidechain":false,"isCompactSummary":true,`+
			`"message":{"role":"user","content":"COMPACTONLYMARKER wipe it all"}}`,
	)
	for _, q := range []string{"VIEWONLYMARKER", "COMPACTONLYMARKER"} {
		_, err := ResolveCitation(path, userReq(q))
		assert.Error(t, err, "%s grounded as the user's words", q)
	}
}
