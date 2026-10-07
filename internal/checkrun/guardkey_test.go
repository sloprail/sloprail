package checkrun

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/checkcache"
	"github.com/sloprail/sloprail/internal/declaration"
)

func twoFilePayload(extra bool) (changeset.Payload, changeset.Payload) {
	cs := changeset.Changeset{
		Commits: []changeset.Commit{{SHA: "c1", Trailers: map[string][]string{changeset.TrailerCitesUser: {"words"}}}},
		Files: []changeset.File{
			{Path: "a.md", Status: "M", Commits: []string{"c1"}, NewContent: "x"},
			{Path: "b.md", Status: "M", Commits: []string{"c1"}, NewContent: "y"},
		},
	}
	if extra {
		// A later commit touching neither file, carrying a citation.
		cs.Commits = append(cs.Commits, changeset.Commit{SHA: "c2", Trailers: map[string][]string{changeset.TrailerCitesTool: {"ls: cannot access"}}})
	}
	a := changeset.NewPayload(cs, changeset.Subject{ID: "a.md", Files: []string{"a.md"}}, "")
	b := changeset.NewPayload(cs, changeset.Subject{ID: "b.md", Files: []string{"b.md"}}, "")
	return a, b
}

// #291: a commit that touches none of a subject's files, whatever citation it carries, must
// not move that subject's key, for a rule that requires a citation and one that does not.
func TestGuardKey_ACommitOutsideTheSubjectDoesNotMoveItsKey(t *testing.T) {
	for _, g := range []declaration.FileGuard{
		{},
		{Require: []declaration.Prerequisite{{Citation: &declaration.CitationPrerequisite{}}}},
	} {
		_ = g
		key := guardKey
		a0, b0 := twoFilePayload(false)
		a1, b1 := twoFilePayload(true)
		assert.Equal(t, key(a0), key(a1))
		assert.Equal(t, key(b0), key(b1))
	}
}

// What grounds the subject's own files does not move its key either: a pass is about the
// content, and a citation is only a gate, asked again on every run. The file's content, and a
// subject's fingerprint, still do.
func TestGuardKey_TheSubjectsOwnCitationsDoNotMoveItsKeyButItsContentDoes(t *testing.T) {
	key := guardKey
	a0, _ := twoFilePayload(false)
	a1, _ := twoFilePayload(false)
	a1.Changeset.Commits[0].Trailers = map[string][]string{changeset.TrailerCitesUser: {"better words"}}
	assert.Equal(t, key(a0), key(a1))

	edited, _ := twoFilePayload(false)
	edited.Changeset.Files[0].NewContent = "x, edited"
	assert.NotEqual(t, key(a0), key(edited), "a file's content moves the key")

	fp, _ := twoFilePayload(false)
	fp.Subject.Fingerprint = "v2"
	assert.NotEqual(t, key(a0), key(fp), "the subject's fingerprint moves the key")
}

// The key's exact bytes, for fixed inputs. IF THIS FAILS, YOU CHANGED THE KEY: bump
// checkcache.SchemaDir (and SchemaVersion), add the old directory to schemaHistory, and make
// sure the rebuild migration (RebuildKeys) covers the change, or every stored verdict is
// judged again. Only then update the ids below.
func TestGuardKey_FixedInputsHaveFixedKeys(t *testing.T) {
	payload := func(files []changeset.File, subject changeset.Subject) changeset.Payload {
		paths := make([]string, 0, len(files))
		for _, f := range files {
			paths = append(paths, f.Path)
		}
		subject.Files = paths
		return changeset.NewPayload(changeset.Changeset{Files: files}, subject, "")
	}
	for name, tc := range map[string]struct {
		p     changeset.Payload
		wantF string // the fingerprint
		wantK string // the cache key id: sr3, rule, kind, subject, fingerprint
	}{
		"one file": {
			p: payload([]changeset.File{{Path: "a.md", Status: "M", NewBlob: "1111111111111111111111111111111111111111"}},
				changeset.Subject{ID: changeset.DefaultSubjectID}),
			wantF: "21836223915d5aba29bd7761dfd2dc0fdcb5c01dac6d951349da0bc5aa4aee03", wantK: "a49b3086889415711c6d677a061803c1cf441820f6eb696493553de7a391e32e",
		},
		"two files and a subject fingerprint": {
			p: payload([]changeset.File{
				{Path: "a.md", Status: "M", NewBlob: "1111111111111111111111111111111111111111"},
				{Path: "docs/b.md", Status: "A", NewBlob: "2222222222222222222222222222222222222222"},
			}, changeset.Subject{ID: "api", Fingerprint: "schema-v7"}),
			wantF: "c21e41170946c0eab54e8624cf13a2163d0519c9a12b1f519c426e854d250b27", wantK: "6400a4fcda3bc06af9ebc3cc6b534d437b65c324126baceb338029bbaf075f59",
		},
		"a deletion": {
			p: payload([]changeset.File{{Path: "gone.md", Status: "D", OldBlob: "3333333333333333333333333333333333333333"}},
				changeset.Subject{ID: "gone.md"}),
			wantF: "37ff6e60ec0dd9192a7fbc5cf86def1c9f88c3c1a355ae33640350f022196b01", wantK: "147ec64499a73ad09aefb48e4ce5ae0008cfc0035d656b1b4b29cc59e88c8141",
		},
	} {
		t.Run(name, func(t *testing.T) {
			fp := guardKey(tc.p)
			assert.Equal(t, tc.wantF, fp, "the fingerprint")
			id := checkcache.Key{Rule: "plugin/file-guard/rule", Kind: guardKind, Subject: tc.p.Subject.ID, Fingerprint: fp}.ID()
			assert.Equal(t, tc.wantK, id, "the cache key id")
		})
	}
}

// What a subject's checks receive matches what its key covers: only the citations
// grounding its own files, never another file's, and an empty list rather than
// null. Reverting the scoping would hand a check citations the key ignores, and a
// stale verdict would be reused (#291).
func TestSubjectChangeset_ChecksSeeOnlyTheSubjectsCitations(t *testing.T) {
	cite := func(q, sha, file string) changeset.Citation {
		var c changeset.Citation
		c.Quote, c.Commits, c.Files = q, []string{sha}, []string{file}
		return c
	}
	cs := changeset.Changeset{
		Commits: []changeset.Commit{{SHA: "c1"}, {SHA: "c2"}},
		Files: []changeset.File{
			{Path: "a.md", Status: "M", Commits: []string{"c1"}},
			{Path: "b.md", Status: "M", Commits: []string{"c2"}},
		},
		Citations: []changeset.Citation{cite("words for a", "c1", "a.md"), cite("words for b", "c2", "b.md")},
	}
	a := subjectChangeset(cs, changeset.Subject{ID: "a.md", Files: []string{"a.md"}})
	assert.Equal(t, cs.EvidenceForSubject(changeset.Subject{ID: "a.md", Files: []string{"a.md"}}), a.Citations)
	for _, c := range a.Citations {
		assert.NotEqual(t, "words for b", c.Quote, "b.md's citation is no input of a.md's checks")
	}
	none := subjectChangeset(cs, changeset.Subject{ID: "c.md", Files: []string{"c.md"}})
	assert.NotNil(t, none.Citations, "never null: a check may iterate it")
	assert.Empty(t, none.Citations)
	assert.Len(t, cs.Citations, 2, "the range's own changeset is left as it was")
}

// Cited commits accumulate: a file changed by two cited commits hands its checks
// both commits' proof (TestReview_CitedEditAfterCitedTransitionKeepsEvidence), though
// that evidence is no part of the stored verdict's key.
func TestSubjectChangeset_EveryCommitOfTheFileIsEvidence(t *testing.T) {
	build := func(first string) changeset.Payload {
		cs := changeset.Changeset{
			Commits: []changeset.Commit{
				{SHA: "c1", Trailers: map[string][]string{changeset.TrailerCitesTool: {first}}},
				{SHA: "c2", Trailers: map[string][]string{changeset.TrailerCitesTool: {"PROOF-TWO"}}},
			},
			Files: []changeset.File{{Path: "a.md", Status: "M", Commits: []string{"c1", "c2"}, NewContent: "x"}},
		}
		TrustTrailers(&cs)
		sub := changeset.Subject{ID: "a.md", Files: []string{"a.md"}}
		return changeset.NewPayload(subjectChangeset(cs, sub), sub, "")
	}
	p := build("PROOF-ONE")
	var quotes []string
	for _, c := range p.Changeset.Citations {
		quotes = append(quotes, c.Quote)
	}
	assert.Equal(t, []string{"PROOF-ONE", "PROOF-TWO"}, quotes, "both steps' proof reaches the checks")

	assert.Equal(t, guardKey(p), guardKey(build("PROOF-ONE-REWORDED")), "the earlier commit's evidence is not in the key")
}
