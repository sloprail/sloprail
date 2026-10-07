package checkrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/guardrail"
)

// A citation requirement with a `when` script decides on more than the trailers, so its rule's
// verdicts are kept per citation set: the same content over other citations is judged again,
// over the same ones it is a hit.
func TestCitationGate_AWhenRulesVerdictIsPerCitationSet(t *testing.T) {
	f := newEvalFixture(t, func(g *declaration.FileGuard) {
		g.Require = []declaration.Prerequisite{{Citation: &declaration.CitationPrerequisite{SourceTypes: []string{"user"}}, When: "./when.sh"}}
	})
	require.NoError(t, os.WriteFile(filepath.Join(f.guard.Dir, "when.sh"), []byte("#!/bin/sh\ncat >/dev/null\nexit 0\n"), 0o755))
	session := filepath.Join(t.TempDir(), "session.jsonl")
	require.NoError(t, os.WriteFile(session, []byte(
		`{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"s1","cwd":"/x","message":{"role":"user","content":"the user said so, and more"}}`+"\n"), 0o644))
	base := runGit(t, f.repo, "rev-parse", "HEAD")
	f.base = base
	land := func(branch, quote string) {
		runGit(t, f.repo, "checkout", "-q", "-b", branch, base)
		require.NoError(t, os.MkdirAll(filepath.Join(f.repo, "docs"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(f.repo, "docs", "a.md"), []byte("clean"), 0o644))
		runGit(t, f.repo, "add", "-A")
		runGit(t, f.repo, "commit", "-m", "doc", "-m", changeset.TrailerCitesUser+": "+quote)
	}
	land("one", "the user said so")
	r, refused := f.evaluateWith(t, session, "b1")
	require.False(t, refused, r.Reason)
	require.Equal(t, 1, f.runs(t))

	land("same", "the user said so")
	r, refused = f.evaluateWith(t, session, "b2")
	require.False(t, refused, r.Reason)
	assert.Equal(t, 1, f.runs(t), "the same citations: a hit")

	land("other", "and more")
	r, refused = f.evaluateWith(t, session, "b3")
	require.False(t, refused, r.Reason)
	assert.Equal(t, 2, f.runs(t), "other citations: judged again")
}

// The digest a migration rebuilds from the range is the one a run computes from the subject's
// own changeset, so a migrated verdict of such a rule is a hit.
func TestCitationDigest_RebuiltEqualsTheRunsOwn(t *testing.T) {
	f := citedFixture(t)
	rng, err := gitrepo.ResolveRange(f.repo, runGit(t, f.repo, "rev-parse", "HEAD~1"), "HEAD")
	require.NoError(t, err)
	match, err := guardrail.CompileFileMatch(f.guard.Match)
	require.NoError(t, err)
	cs, err := changeset.Build(f.repo, rng, changeset.Options{Scan: Markers, Select: Selector(match)})
	require.NoError(t, err)
	sub := changeset.Whole(cs)
	rebuilt := citationDigest(changeset.NewPayload(cs, sub, ""))
	run := citationDigest(changeset.NewPayload(subjectChangeset(cs, sub), sub, ""))
	assert.Equal(t, run, rebuilt)
	empty := citationDigest(changeset.NewPayload(changeset.Changeset{Files: cs.Files}, sub, ""))
	assert.NotEqual(t, rebuilt, empty, "the digest does cover the citations")
}
