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
	return NewPayload(cs, Whole(cs), "/t.jsonl")
}

func cp(t *testing.T, p Payload) string {
	t.Helper()
	s, err := CitationPart(p)
	require.NoError(t, err)
	return s
}

func TestGuardFingerprint_IsDeterministicAndFilesSensitive(t *testing.T) {
	assert.Equal(t, GuardFingerprint("f", "", ""), GuardFingerprint("f", "", ""))
	assert.NotEqual(t, GuardFingerprint("f", "", ""), GuardFingerprint("g", "", ""))
}

// What a subject declares is added to the key, and parts cannot be re-cut.
func TestGuardFingerprint_SubjectFingerprintMovesTheKey(t *testing.T) {
	assert.NotEqual(t, GuardFingerprint("f", "", ""), GuardFingerprint("f", "v1", ""))
	assert.NotEqual(t, GuardFingerprint("f", "v1", ""), GuardFingerprint("f", "v2", ""))
	assert.NotEqual(t, GuardFingerprint("ab", "c", ""), GuardFingerprint("a", "bc", ""))
	assert.NotEqual(t, GuardFingerprint("f", "a", ""), GuardFingerprint("f", "", "a"))
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

// Only the quotes (and their pools) that ground the subject are keyed. Rewording an unrelated
// commit message, or adding a commit, is not an input.
func TestCitationPart_QuotesMatter(t *testing.T) {
	for name, mutate := range map[string]func(*Payload){
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

func TestCitationPart_CommitMessagesAreNotInputs(t *testing.T) {
	for name, mutate := range map[string]func(*Payload){
		"subject":      func(p *Payload) { p.Changeset.Commits[0].Subject = "different" },
		"body":         func(p *Payload) { p.Changeset.Commits[0].Body = "different" },
		"trailer":      func(p *Payload) { p.Changeset.Commits[0].Trailers[TrailerCitesUser] = []string{"z"} },
		"extra commit": func(p *Payload) { p.Changeset.Commits = append(p.Changeset.Commits, Commit{Subject: "more"}) },
		"commit count": func(p *Payload) { p.Changeset.Citations[0].Commits = []string{"c1", "c0"} },
	} {
		t.Run(name, func(t *testing.T) {
			a, b := samplePayload(), samplePayload()
			mutate(&b)
			assert.Equal(t, cp(t, a), cp(t, b))
		})
	}
}

// A citation that grounds another subject's file is not this subject's input.
func TestCitationPart_OnlyTheSubjectsCitations(t *testing.T) {
	a, b := samplePayload(), samplePayload()
	b.Changeset.Citations = append(b.Changeset.Citations, Citation{Citation: transcript.Citation{Quote: "elsewhere"}, Commits: []string{"c9"}})
	assert.Equal(t, cp(t, a), cp(t, b))
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
		return NewPayload(cs, Whole(cs), "")
	}
	a, b := build("2026-01-01T00:00:00Z"), build("2026-02-02T00:00:00Z")
	require.NotEqual(t, a.Changeset.Head, b.Changeset.Head)
	assert.Equal(t, cp(t, a), cp(t, b))
}

// The key is over blob ids: the same blob at the same path is the same key without any bytes read.
func TestFilesPart_FromBlobIDsNotBytes(t *testing.T) {
	mk := func(blob, content string) Payload {
		return Payload{Subject: Subject{Files: []string{"a.md"}}, Changeset: Changeset{Files: []File{{Path: "a.md", Status: "M", NewBlob: blob, NewContent: content}}}}
	}
	assert.Equal(t, FilesPart(mk("b1", "")), FilesPart(mk("b1", "x")))
	assert.NotEqual(t, FilesPart(mk("b1", "")), FilesPart(mk("b2", "")))
}

// A FILE subject is keyed by its files' content AND by the fingerprint its `subjects:` script
// gave (additive): either moving makes a new key.
func TestGuardKey_FileSubjectPlusFingerprintIsAdditive(t *testing.T) {
	key := func(content, fp string) string {
		p := samplePayload()
		p.Changeset.Files[0].NewContent = content
		p.Subject = Subject{ID: "a", Files: []string{"a.go"}, Fingerprint: fp}
		return GuardFingerprint(FilesPart(p), p.Subject.Fingerprint, "")
	}
	assert.Equal(t, key("2", "v1"), key("2", "v1"))
	assert.NotEqual(t, key("2", "v1"), key("2", "v2"), "the script's fingerprint moved")
	assert.NotEqual(t, key("2", "v1"), key("3", "v1"), "the content moved")
	assert.NotEqual(t, key("2", ""), key("2", "v1"), "a fingerprint is added to the content, not instead of it")
}

// An FQN subject (no files) is keyed by its fingerprint alone, whatever the changeset holds.
func TestGuardKey_FQNSubjectIsItsFingerprint(t *testing.T) {
	key := func(content, fp string) string {
		p := samplePayload()
		p.Changeset.Files[0].NewContent = content
		p.Subject = Subject{ID: "Billing.Invoice", Fingerprint: fp}
		return GuardFingerprint(FilesPart(p), p.Subject.Fingerprint, "")
	}
	assert.Equal(t, key("2", "v1"), key("other bytes", "v1"), "no file, so no file content in the key")
	assert.NotEqual(t, key("2", "v1"), key("2", "v2"), "its fingerprint moved")
}

func TestRangeCitationPart_QuotesMoveItAndDuplicatesDoNot(t *testing.T) {
	a, b := samplePayload(), samplePayload()
	assert.Equal(t, RangeCitationPart(a.Changeset), RangeCitationPart(b.Changeset))
	b.Changeset.Citations = append(b.Changeset.Citations, Citation{Citation: transcript.Citation{Quote: "better words", SourceTypes: []transcript.SourceType{transcript.SourceUser}}})
	assert.NotEqual(t, RangeCitationPart(a.Changeset), RangeCitationPart(b.Changeset))
	b.Changeset.Citations = append(b.Changeset.Citations, b.Changeset.Citations[0])
	c := samplePayload()
	c.Changeset.Citations = append(c.Changeset.Citations, b.Changeset.Citations[1])
	assert.Equal(t, RangeCitationPart(c.Changeset), RangeCitationPart(b.Changeset))
}

// A deletion is keyed by what was deleted: the same path deleted with other old content is another change.
func TestFilesPart_DeletedFileKeyHasItsOldBlob(t *testing.T) {
	mk := func(oldBlob, oldContent string) Payload {
		return Payload{Subject: Subject{Files: []string{"a.md"}}, Changeset: Changeset{Files: []File{{Path: "a.md", Status: "D", OldBlob: oldBlob, OldContent: oldContent}}}}
	}
	assert.Equal(t, FilesPart(mk("b1", "")), FilesPart(mk("b1", "x")), "the blob id stands for the bytes")
	assert.NotEqual(t, FilesPart(mk("b1", "")), FilesPart(mk("b2", "")), "another old blob is another deletion")
	assert.NotEqual(t, FilesPart(mk("", "A")), FilesPart(mk("", "B")), "without a blob, the old bytes")
}
