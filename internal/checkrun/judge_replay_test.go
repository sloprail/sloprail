package checkrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

// A refusal by a JUDGE is replayed on `run`: asking again would pay for the same judge, and
// repeated runs over still-failing content must never re-judge. (A refusal by a citation
// requirement or a script is cheap and is checked again: see the other tests of this file's
// neighbours.)
func TestEvaluate_AJudgeRefusalIsReplayedWithoutAskingTheJudgeAgain(t *testing.T) {
	var ledger string
	f := newEvalFixture(t, func(g *declaration.FileGuard) {
		g.Checks = []declaration.Check{{Judge: "j.md.j2", Model: "m"}}
		require.NoError(t, os.WriteFile(filepath.Join(g.Dir, "j.md.j2"), []byte("Judge {{ change }}\n"), 0o644))
		ledger = filepath.Join(t.TempDir(), "judge-calls")
		mock := filepath.Join(g.Dir, "mock.sh")
		require.NoError(t, os.WriteFile(mock, []byte("#!/bin/sh\necho call >> '"+ledger+"'\necho '{\"pass\": false, \"reasoning\": \"not good enough\"}'\n"), 0o755))
		t.Setenv("SR_CHECKS_JUDGE_MOCKS", `{"file-guard/docs/j":"`+mock+`"}`)
	}).withSession(t)
	f.commitDoc(t, "docs/a.md", "content")
	calls := func() int {
		b, _ := os.ReadFile(ledger)
		return len(b) / len("call\n")
	}

	r, refused := f.evaluate(t, f.results)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "not good enough")
	require.Equal(t, 1, calls())

	for i := 0; i < 3; i++ {
		r, refused = f.evaluate(t, f.results)
		require.True(t, refused, "still refused")
		assert.Contains(t, r.Reason, "not good enough")
	}
	assert.Equal(t, 1, calls(), "the judge was not asked again")

	f.commitDoc(t, "docs/a.md", "other content")
	_, _ = f.evaluate(t, f.results)
	assert.Equal(t, 2, calls(), "other content is a miss")
}
