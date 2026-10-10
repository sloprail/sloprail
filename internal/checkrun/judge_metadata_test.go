package checkrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

// metadataFixture is a rule whose judge check has a post_process returning the judge's
// answer as metadata, behind a mock that answers `answer` and counts its calls.
func metadataFixture(t *testing.T, answer string) (f *evalFixture, calls func() int) {
	t.Helper()
	var ledger string
	f = newEvalFixture(t, func(g *declaration.FileGuard) {
		g.Checks = []declaration.Check{{Judge: "j.md.j2", Model: "m", PostProcess: "./post-process.sh"}}
		require.NoError(t, os.WriteFile(filepath.Join(g.Dir, "j.md.j2"), []byte("Judge {{ change }}\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(g.Dir, "post-process.sh"),
			[]byte("#!/bin/sh\njq -c '{pass: .pass, reasoning: .reasoning, metadata: {table: {\"a/one\": (if .pass then \"pass\" else \"fail\" end)}, n: 2}}'\n"), 0o755))
		ledger = filepath.Join(t.TempDir(), "judge-calls")
		mock := filepath.Join(g.Dir, "mock.sh")
		require.NoError(t, os.WriteFile(mock, []byte("#!/bin/sh\ncat >/dev/null\necho call >> '"+ledger+"'\necho '"+answer+"'\n"), 0o755))
		t.Setenv("SR_CHECKS_JUDGE_MOCKS", `{"file-guard/docs/j":"`+mock+`"}`)
	}).withSession(t)
	f.commitDoc(t, "docs/a.md", "content")
	return f, func() int {
		b, _ := os.ReadFile(ledger)
		return len(b) / len("call\n")
	}
}

// judgeOutcome is the judge step among an evaluation's outcomes.
func judgeOutcome(t *testing.T, outcomes []CheckOutcome) CheckOutcome {
	t.Helper()
	for _, o := range outcomes {
		if strings.Contains(o.Kind, ":judge:") {
			return o
		}
	}
	t.Fatalf("no judge step among %+v", outcomes)
	return CheckOutcome{}
}

// What a judge's post_process returned as metadata is stored with the subject's verdict,
// a pass and a fail alike, and every later reading of that verdict (show, verify, a run
// that replays it) returns the same metadata without the judge being asked again.
// sr:proves cache/stored-verdict-keeps-metadata
func TestEvaluate_AStoredVerdictKeepsItsJudgesMetadata(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		f, calls := metadataFixture(t, `{"pass": true, "reasoning": "all held"}`)
		want := map[string]any{"table": map[string]any{"a/one": "pass"}, "n": float64(2)}

		refusals, outcomes := f.evaluateOver(t, f.base, "HEAD", false, false)
		require.Empty(t, refusals)
		ran := judgeOutcome(t, outcomes)
		assert.Equal(t, "ran", ran.Source)
		assert.Equal(t, want, ran.Metadata, "the run reports what the post_process returned")

		// show: the whole range, read from the store.
		_, outcomes = f.evaluateOver(t, f.base, "HEAD", true, true)
		shown := judgeOutcome(t, outcomes)
		assert.Equal(t, "pass", shown.Status)
		assert.NotEqual(t, "ran", shown.Source)
		assert.Equal(t, want, shown.Metadata, "a stored pass returns its metadata")

		// The same content on another branch is the same verdict: verify reads it and a run
		// replays it, each with the metadata, and the judge is not asked.
		ruleCommit := runGit(t, f.repo, "rev-parse", "HEAD~1")
		runGit(t, f.repo, "checkout", "-q", "-b", "other", ruleCommit)
		runGit(t, f.repo, "commit", "-q", "--allow-empty", "-m", "another history")
		f.commitDoc(t, "docs/a.md", "content")
		_, outcomes = f.evaluateOver(t, f.base, "HEAD", true, false)
		verified := judgeOutcome(t, outcomes)
		assert.Equal(t, "pass", verified.Status)
		assert.Equal(t, want, verified.Metadata, "verify reads a stored pass with its metadata")
		refusals, outcomes = f.evaluateOver(t, f.base, "HEAD", false, false)
		require.Empty(t, refusals)
		replayed := judgeOutcome(t, outcomes)
		assert.Equal(t, "cached", replayed.Source)
		assert.Equal(t, want, replayed.Metadata, "a run that replays a stored pass returns its metadata")
		assert.Equal(t, 1, calls(), "the judge was asked once in all")
	})
	t.Run("fail", func(t *testing.T) {
		f, calls := metadataFixture(t, `{"pass": false, "reasoning": "a/one is not proven"}`)
		want := map[string]any{"table": map[string]any{"a/one": "fail"}, "n": float64(2)}

		refusals, outcomes := f.evaluateOver(t, f.base, "HEAD", false, false)
		require.Len(t, refusals, 1)
		assert.Contains(t, refusals[0].Reason, "a/one is not proven")
		assert.NotContains(t, refusals[0].Reason, "table", "metadata is stored, not shown to the agent")
		assert.Equal(t, want, judgeOutcome(t, outcomes).Metadata, "a fail keeps its metadata too")

		// A second run replays the stored fail: a cache hit, the metadata with it.
		_, outcomes = f.evaluateOver(t, f.base, "HEAD", false, false)
		again := judgeOutcome(t, outcomes)
		assert.Equal(t, "cached", again.Source)
		assert.Equal(t, want, again.Metadata, "a cache hit returns the stored metadata")

		for name, whole := range map[string]bool{"verify": false, "show": true} {
			_, outcomes = f.evaluateOver(t, f.base, "HEAD", true, whole)
			read := judgeOutcome(t, outcomes)
			assert.Equal(t, "fail", read.Status, name)
			assert.Equal(t, want, read.Metadata, "%s reads the stored metadata", name)
		}
		assert.Equal(t, 1, calls(), "the judge was asked once in all")
	})
}

// A check with no post_process stores no metadata: the record is what it was.
// sr:proves cache/stored-verdict-keeps-metadata
func TestEvaluate_ACheckWithoutPostProcessStoresNoMetadata(t *testing.T) {
	f := newEvalFixture(t, func(g *declaration.FileGuard) {
		g.Checks = []declaration.Check{{Judge: "j.md.j2", Model: "m"}}
		require.NoError(t, os.WriteFile(filepath.Join(g.Dir, "j.md.j2"), []byte("Judge {{ change }}\n"), 0o644))
		mock := filepath.Join(g.Dir, "mock.sh")
		require.NoError(t, os.WriteFile(mock, []byte("#!/bin/sh\ncat >/dev/null\necho '{\"pass\": true, \"reasoning\": \"fine\"}'\n"), 0o755))
		t.Setenv("SR_CHECKS_JUDGE_MOCKS", `{"file-guard/docs/j":"`+mock+`"}`)
	}).withSession(t)
	f.commitDoc(t, "docs/a.md", "content")
	_, outcomes := f.evaluateOver(t, f.base, "HEAD", false, false)
	assert.Nil(t, judgeOutcome(t, outcomes).Metadata)
	_, outcomes = f.evaluateOver(t, f.base, "HEAD", true, true)
	assert.Nil(t, judgeOutcome(t, outcomes).Metadata)
}
