package changeset

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/transcript"
)

func samplePayload() Payload {
	cs := Changeset{
		Base: "b0", Head: "h0",
		Commits:   []Commit{{SHA: "c1", Subject: "split", Body: "b", Trailers: map[string][]string{TrailerCitesUser: {"q"}}}},
		Files:     []File{{Path: "a.go", Status: "M", Commits: []string{"c1"}, OldContent: "1", NewContent: "2", OldMarkers: []Marker{}, NewMarkers: []Marker{{Kind: "k", FQN: "f", Line: 1}}, Diff: "@@"}},
		Others:    []Other{{Path: "README.md", Status: "M"}},
		Citations: []Citation{{Citation: transcript.Citation{Quote: "q", SourceTypes: []transcript.SourceType{transcript.SourceUser}, Path: "/t", Line: 3, Message: "m"}, Commits: []string{"c1"}, Files: []string{"a.go"}}},
	}
	return NewPayload(cs, Whole(cs), "/t.jsonl", nil)
}

func cp(t *testing.T, p Payload) string {
	t.Helper()
	s, err := CitationPart(p)
	require.NoError(t, err)
	return s
}

func TestJudgeFingerprint_IsDeterministicAndTemplateAndFilesSensitive(t *testing.T) {
	assert.Equal(t, JudgeFingerprint("t", "f", "", ""), JudgeFingerprint("t", "f", "", ""))
	assert.NotEqual(t, JudgeFingerprint("t", "f", "", ""), JudgeFingerprint("u", "f", "", ""))
	assert.NotEqual(t, JudgeFingerprint("t", "f", "", ""), JudgeFingerprint("t", "g", "", ""))
}

// What prepare declares is added to the key, and parts cannot be re-cut.
func TestJudgeFingerprint_PrepareFingerprintMovesTheKey(t *testing.T) {
	assert.NotEqual(t, JudgeFingerprint("t", "f", "", ""), JudgeFingerprint("t", "f", "v1", ""))
	assert.NotEqual(t, JudgeFingerprint("t", "f", "v1", ""), JudgeFingerprint("t", "f", "v2", ""))
	assert.NotEqual(t, JudgeFingerprint("ab", "c", "", ""), JudgeFingerprint("a", "bc", "", ""))
	assert.NotEqual(t, JudgeFingerprint("t", "f", "a", ""), JudgeFingerprint("t", "f", "", "a"))
}

// The matched files' content is keyed; a SHA, a base or a transcript never is.
func TestFilesPart_ContentOnly(t *testing.T) {
	a, b := samplePayload(), samplePayload()
	b.Changeset.Base, b.Changeset.Head, b.TranscriptPath = "x", "y", "/other.jsonl"
	b.Changeset.Commits[0].SHA, b.Changeset.Files[0].Commits = "rewritten", []string{"rewritten"}
	b.Changeset.Files[0].Diff = "@@ different base"
	assert.Equal(t, FilesPart(a), FilesPart(b))
	b.Changeset.Files[0].NewContent = "3"
	assert.NotEqual(t, FilesPart(a), FilesPart(b))
	b = samplePayload()
	b.Changeset.Files[0].Status = "D"
	assert.NotEqual(t, FilesPart(a), FilesPart(b))
}

// Rewording a commit or a citation is an input for a rule that reads citations; a SHA never.
func TestCitationPart_CommitMessagesAndQuotesMatter(t *testing.T) {
	for name, mutate := range map[string]func(*Payload){
		"subject":        func(p *Payload) { p.Changeset.Commits[0].Subject = "different" },
		"body":           func(p *Payload) { p.Changeset.Commits[0].Body = "different" },
		"trailer":        func(p *Payload) { p.Changeset.Commits[0].Trailers[TrailerCitesUser] = []string{"z"} },
		"extra commit":   func(p *Payload) { p.Changeset.Commits = append(p.Changeset.Commits, Commit{Subject: "more"}) },
		"citation quote": func(p *Payload) { p.Changeset.Citations[0].Citation.Quote = "another quote" },
		"citation pool": func(p *Payload) {
			p.Changeset.Citations[0].Citation.SourceTypes = []transcript.SourceType{transcript.SourceToolResult}
		},
		"no citation": func(p *Payload) { p.Changeset.Citations = nil },
	} {
		t.Run(name, func(t *testing.T) {
			a, b := samplePayload(), samplePayload()
			mutate(&b)
			assert.NotEqual(t, cp(t, a), cp(t, b))
		})
	}
}

func TestCitationPart_NeverSHAsNorWhereAQuoteWasFound(t *testing.T) {
	a, b := samplePayload(), samplePayload()
	b.Changeset.Base, b.Changeset.Head = "other-base", "other-head"
	b.Changeset.Commits[0].SHA = "rewritten"
	b.TranscriptPath = "/elsewhere.jsonl"
	c := &b.Changeset.Citations[0].Citation
	c.Path, c.Line, c.Message, c.Call = "/elsewhere", 99, "the cited message", "Bash: cat CHANGELOG.md"
	assert.Equal(t, cp(t, a), cp(t, b))
	assert.Equal(t, "c1", a.Changeset.Commits[0].SHA, "the caller's payload is left alone")
}

func TestCitationPart_TrailerKeyOrderDoesNotMatter(t *testing.T) {
	a, b := samplePayload(), samplePayload()
	a.Changeset.Commits[0].Trailers = map[string][]string{"A": {"1"}, "B": {"2"}}
	b.Changeset.Commits[0].Trailers = map[string][]string{"B": {"2"}, "A": {"1"}}
	assert.Equal(t, cp(t, a), cp(t, b))
}

// Real repositories: the same content committed twice, different SHAs.
func TestCitationPart_ARebuiltRangeWithTheSameContentHits(t *testing.T) {
	build := func(date string) Payload {
		t.Setenv("GIT_AUTHOR_DATE", date)
		t.Setenv("GIT_COMMITTER_DATE", date)
		dir := initRepo(t)
		base := put(t, dir, "seed", map[string]string{"a.go": "1\n"})
		head := put(t, dir, "edit", map[string]string{"a.go": "2\n"})
		cs, err := Build(dir, rng(base, head), Options{Scan: scan, Select: selectAll})
		require.NoError(t, err)
		return NewPayload(cs, Whole(cs), "", nil)
	}
	a, b := build("2026-01-01T00:00:00Z"), build("2026-02-02T00:00:00Z")
	require.NotEqual(t, a.Changeset.Head, b.Changeset.Head)
	assert.Equal(t, cp(t, a), cp(t, b))
}
