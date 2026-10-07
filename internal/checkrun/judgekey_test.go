package checkrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/transcript"
)

// The key a judge's verdict is stored under is computed by `run` before the judge and by
// `verify` afterwards, each over a snapshot in its own temp directory: both must reach the
// same key, and it must move only with what the judge is asked.

func keyPayload() changeset.Payload {
	cs := changeset.Changeset{
		Base: "b0", Head: "h0",
		Commits: []changeset.Commit{{SHA: "c1", Subject: "split", Trailers: map[string][]string{changeset.TrailerCitesUser: {"q"}}}},
		Files:   []changeset.File{{Path: "a.go", Status: "M", Commits: []string{"c1"}, NewContent: "2", Diff: "@@ x"}},
		Citations: []changeset.Citation{{Citation: transcript.Citation{Quote: "q", Path: "/t", Line: 3, Call: "Bash: one"},
			Commits: []string{"c1"}, Files: []string{"a.go"}}},
	}
	return changeset.NewPayload(cs, changeset.Whole(cs), "/t.jsonl")
}

func keyRule(t *testing.T, citation bool) (declaration.FileGuard, declaration.Check, string) {
	t.Helper()
	dir := t.TempDir()
	// The prompt renders the slice and prepare's context (which may carry the snapshot path),
	// and never the commits or the citations.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "j.md.j2"), []byte("Judge:\n{{ change }}\nctx: {{ additionalContext.where }}\n"), 0o644))
	g := declaration.FileGuard{Name: "r", Dir: dir}
	if citation {
		g.Require = []declaration.Prerequisite{{Citation: &declaration.CitationPrerequisite{}}}
	}
	return g, declaration.Check{Judge: "j.md.j2", Model: "m"}, dir
}

// key is the guard's key over a payload; tree and prepFP are accepted only so the cases below
// read as before: neither may matter (the snapshot's path and prepare's context are not input;
// the subject's fingerprint is, and is set on the payload's subject).
func key(t *testing.T, g declaration.FileGuard, _ declaration.Check, p changeset.Payload, _, subjectFP string) string {
	t.Helper()
	p.Subject.Fingerprint = subjectFP
	fp, err := guardKey(g, p)
	require.NoError(t, err)
	return fp
}

// sr:proves cache/verdict-identity
func TestGuardKey_SameCheckInTwoSnapshotDirsKeysTheSame(t *testing.T) {
	g, c, _ := keyRule(t, true)
	assert.Equal(t, key(t, g, c, keyPayload(), "/tmp/sr-tree-111", ""), key(t, g, c, keyPayload(), "/tmp/sr-tree-222", ""))
}

// A subjects script's fingerprint is added to the key; no fingerprint is today's default.
// sr:proves cache/verdict-identity
func TestGuardKey_ASubjectFingerprintChangesTheKey(t *testing.T) {
	g, c, _ := keyRule(t, false)
	none := key(t, g, c, keyPayload(), "", "")
	assert.NotEqual(t, none, key(t, g, c, keyPayload(), "", "v1"))
	assert.NotEqual(t, key(t, g, c, keyPayload(), "", "v1"), key(t, g, c, keyPayload(), "", "v2"))
	assert.Equal(t, none, key(t, g, c, keyPayload(), "", ""), "no fingerprint is the default subject's key")
}

// The key is the same in `run` (a session, a transcript) and in `verify` (neither).
func TestGuardKey_RunAndVerifyComputeOneKey(t *testing.T) {
	g, c, _ := keyRule(t, true)
	run, verify := keyPayload(), keyPayload()
	verify.TranscriptPath = ""
	verify.Changeset.Citations[0].Citation.Path, verify.Changeset.Citations[0].Citation.Line = "", 0
	verify.Changeset.Citations[0].Citation.Call = ""
	assert.Equal(t, key(t, g, c, run, "/t1", "fp"), key(t, g, c, verify, "/t2", "fp"))
}

// sr:proves cache/verdict-identity
func TestGuardKey_ChangingTheSliceChangesTheKey(t *testing.T) {
	g, c, _ := keyRule(t, false)
	other := keyPayload()
	other.Changeset.Files[0].NewContent = "3"
	other.Changeset.Files[0].Diff = "@@ y"
	// `change` is derived from the files' diffs.
	assert.NotEqual(t, key(t, g, c, keyPayload(), "/t1", ""), key(t, g, c, other, "/t1", ""))
}

// A citation reword is an input for every rule (any check can read the range's citations). The volatile
// Call field and SHAs never are.
// sr:proves cache/verdict-identity
func TestGuardKey_ACitationRewordChangesTheKey(t *testing.T) {
	reword := func(p *changeset.Payload) {
		p.Changeset.Commits[0].Trailers[changeset.TrailerCitesUser] = []string{"another"}
		p.Changeset.Citations[0].Citation.Quote = "another"
	}
	volatile := func(p *changeset.Payload) {
		p.Changeset.Citations[0].Citation.Call = "Bash: two"
		p.Changeset.Commits[0].SHA = "rewritten"
		p.Changeset.Files[0].Commits = []string{"rewritten"}
	}
	for _, citation := range []bool{true, false} {
		g, c, _ := keyRule(t, citation)
		base := key(t, g, c, keyPayload(), "/t1", "")
		r, v := keyPayload(), keyPayload()
		reword(&r)
		volatile(&v)
		assert.Equal(t, base, key(t, g, c, v, "/t1", ""), "Call and SHAs are never input")
		msg := keyPayload()
		msg.Changeset.Commits[0].Subject, msg.Changeset.Commits[0].Body = "reworded", "and a body"
		assert.Equal(t, base, key(t, g, c, msg, "/t1", ""), "a commit's subject and body are never input")
		// The subject's own grounding quote is what its checks receive, so a reword moves its key.
		assert.NotEqual(t, base, key(t, g, c, r, "/t1", ""))
	}
}

// The key is the template, the matched files' content, prepare's fingerprint and (citation
// rules) the quotes. Not the rendered prompt, not prepare's context, not the transcript, not
// the history.
// sr:proves cache/verdict-identity
func TestGuardKey_AFileChangeTheTemplateDoesNotRenderChangesTheKey(t *testing.T) {
	g, c, dir := keyRule(t, false)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "j.md.j2"), []byte("Judge the file at {{ subject.id }}.\n"), 0o644))
	other := keyPayload()
	other.Changeset.Files[0].NewContent = "3"
	assert.NotEqual(t, key(t, g, c, keyPayload(), "/t1", ""), key(t, g, c, other, "/t1", ""))
}

// Two branches carrying identical content share their verdicts: SHAs, the range's ends and
// the commits' own identities are not input.
func TestGuardKey_TwoBranchesWithIdenticalContentShareTheKey(t *testing.T) {
	for _, citation := range []bool{true, false} {
		g, c, _ := keyRule(t, citation)
		a, b := keyPayload(), keyPayload()
		b.Changeset.Base, b.Changeset.Head = "other-base", "other-head"
		b.Changeset.Commits[0].SHA = "rebased"
		b.Changeset.Files[0].Commits = []string{"rebased"}
		b.Changeset.Citations[0].Commits = []string{"rebased"}
		assert.Equal(t, key(t, g, c, a, "/tmp/sr-tree-1", ""), key(t, g, c, b, "/tmp/sr-tree-2", ""))
	}
}
