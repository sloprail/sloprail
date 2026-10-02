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

func fp(t *testing.T, p Payload, extra ...string) string {
	t.Helper()
	s, err := Fingerprint(p, "rule-hash", "size-md", extra...)
	require.NoError(t, err)
	return s
}

func TestFingerprint_IsDeterministic(t *testing.T) {
	assert.Equal(t, fp(t, samplePayload()), fp(t, samplePayload()))
}

// A rebase or amend rewrites every SHA and no content, and must still hit.
func TestFingerprint_NeverDependsOnASHA(t *testing.T) {
	a, b := samplePayload(), samplePayload()
	b.Changeset.Base, b.Changeset.Head = "other-base", "other-head"
	b.Changeset.Commits[0].SHA = "rewritten"
	assert.Equal(t, fp(t, a), fp(t, b))
}

// A squash changes the commits and their messages and none of the content.
func TestFingerprint_HistoryIsNotInput(t *testing.T) {
	a, b := samplePayload(), samplePayload()
	b.Changeset.Commits = []Commit{{SHA: "s", Subject: "squashed"}, {SHA: "t", Subject: "and another"}}
	b.Changeset.Files[0].Commits = []string{"s", "t"}
	b.Changeset.Citations[0].Commits = []string{"s"}
	assert.Equal(t, fp(t, a), fp(t, b))
}

func TestFingerprint_DoesNotChangeTheCallersPayload(t *testing.T) {
	p := samplePayload()
	fp(t, p)
	assert.Equal(t, "h0", p.Changeset.Head)
	assert.Equal(t, "c1", p.Changeset.Commits[0].SHA)
	assert.Equal(t, "/t.jsonl", p.TranscriptPath)
}

func TestFingerprint_TheTranscriptLocationIsNotInput(t *testing.T) {
	a, b := samplePayload(), samplePayload()
	b.TranscriptPath = "/elsewhere.jsonl"
	assert.Equal(t, fp(t, a), fp(t, b))
}

// Everything the check receives must move the fingerprint.
func TestFingerprint_EverythingTheCheckReceivesMattersToIt(t *testing.T) {
	base := fp(t, samplePayload())
	for name, mutate := range map[string]func(*Payload){
		"new content":     func(p *Payload) { p.Changeset.Files[0].NewContent = "3" },
		"old content":     func(p *Payload) { p.Changeset.Files[0].OldContent = "0" },
		"path":            func(p *Payload) { p.Changeset.Files[0].Path = "b.go" },
		"status":          func(p *Payload) { p.Changeset.Files[0].Status = "A" },
		"old path":        func(p *Payload) { p.Changeset.Files[0].OldPath = "z.go" },
		"diff":            func(p *Payload) { p.Changeset.Files[0].Diff = "@@ changed" },
		"marker":          func(p *Payload) { p.Changeset.Files[0].NewMarkers[0].FQN = "g" },
		"old marker":      func(p *Payload) { p.Changeset.Files[0].OldMarkers = []Marker{{Kind: "k"}} },
		"extra file":      func(p *Payload) { p.Changeset.Files = append(p.Changeset.Files, File{Path: "c.go"}) },
		"other":           func(p *Payload) { p.Changeset.Others[0].Status = "D" },
		"citation":        func(p *Payload) { p.Changeset.Citations[0].Citation.Message = "other message" },
		"no citation":     func(p *Payload) { p.Changeset.Citations = nil },
		"subject id":      func(p *Payload) { p.Subject.ID = "pkg/a" },
		"subject files":   func(p *Payload) { p.Subject.Files = []string{"a.go", "x.go"} },
		"subject context": func(p *Payload) { p.Subject.Context = map[string]any{"k": "v"} },
		"context":         func(p *Payload) { p.Context = map[string]any{"goal": true} },
	} {
		p := samplePayload()
		mutate(&p)
		assert.NotEqual(t, base, fp(t, p), "changing the %s must change the fingerprint", name)
	}
}

func TestFingerprint_ExtraPartsAreFramed(t *testing.T) {
	p := samplePayload()
	assert.NotEqual(t, fp(t, p), fp(t, p, "prepared"), "what prepare inlined is part of the input")
	assert.NotEqual(t, fp(t, p, "a", "bc"), fp(t, p, "ab", "c"), "parts cannot be re-cut")
	assert.NotEqual(t, fp(t, p, "a"), fp(t, p, "a", ""))
}

func TestFingerprint_TrailerKeyOrderDoesNotMatter(t *testing.T) {
	a, b := samplePayload(), samplePayload()
	a.Changeset.Commits[0].Trailers = map[string][]string{"A": {"1"}, "B": {"2"}}
	b.Changeset.Commits[0].Trailers = map[string][]string{"B": {"2"}, "A": {"1"}}
	assert.Equal(t, fp(t, a), fp(t, b))
}

// Real repositories: the same content committed twice, different SHAs.
func TestFingerprint_ARebuiltRangeWithTheSameContentHits(t *testing.T) {
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
	require.NotEqual(t, a.Changeset.Head, b.Changeset.Head, "the two histories really do have different SHAs")
	assert.Equal(t, fp(t, a), fp(t, b))
}

func TestFingerprint_TheRuleHashAndTheModelAreInTheKey(t *testing.T) {
	p := samplePayload()
	base, err := Fingerprint(p, "h1", "size-md")
	require.NoError(t, err)
	edited, err := Fingerprint(p, "h2", "size-md")
	require.NoError(t, err)
	other, err := Fingerprint(p, "h1", "size-lg")
	require.NoError(t, err)
	assert.NotEqual(t, base, edited, "editing anything in the rule's folder must miss")
	assert.NotEqual(t, base, other, "a different model must miss")
	swapped, err := Fingerprint(p, "size-md", "h1")
	require.NoError(t, err)
	assert.NotEqual(t, base, swapped, "rule hash and model are separate parts")
}
