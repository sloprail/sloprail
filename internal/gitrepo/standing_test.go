package gitrepo

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commitAt commits a file with an explicit committer date.
func commitAt(t *testing.T, dir, rel, body string, when time.Time) string {
	t.Helper()
	t.Setenv("GIT_COMMITTER_DATE", when.Format(time.RFC3339))
	t.Setenv("GIT_AUTHOR_DATE", when.Format(time.RFC3339))
	return commitIn(t, dir, rel, body)
}

func TestRaiseBaseToTime_OnlyCommitsMadeAtOrAfterTheInstantRemain(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "seed.txt", "s")
	t0 := time.Now().Add(-10 * time.Hour).Truncate(time.Second)
	old := commitAt(t, dir, "a.txt", "1", t0)
	_ = commitAt(t, dir, "b.txt", "2", t0.Add(2*time.Hour))
	head := commitAt(t, dir, "c.txt", "3", t0.Add(4*time.Hour))

	r, err := RaiseBaseToTime(dir, Range{Base: base, Head: head}, t0.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, old, r.Base, "the newest commit before the instant becomes the base")

	// A commit in the same second counts as after: the stricter side.
	r, err = RaiseBaseToTime(dir, Range{Base: base, Head: head}, t0)
	require.NoError(t, err)
	assert.Equal(t, base, r.Base)

	// The base never moves earlier.
	r, err = RaiseBaseToTime(dir, Range{Base: head, Head: head}, t0.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, head, r.Base)
	r, err = RaiseBaseToTime(dir, Range{Base: head, Head: git(t, dir, "rev-parse", "HEAD")}, t0.Add(3*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, head, r.Base)
}

func TestRuleAbsentFromLine(t *testing.T) {
	dir := initRepo(t)
	line := commit(t, dir, "seed.txt", "s")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "match: '*.go'")
	head := git(t, dir, "rev-parse", "HEAD")

	assert.True(t, RuleAbsentFromLine(dir, line, ruleDir), "a line that predates the rule")
	assert.False(t, RuleAbsentFromLine(dir, head, ruleDir), "a line that has it")
	assert.False(t, RuleAbsentFromLine(dir, line, ""), "a rule outside the repository")

	git(t, dir, "rm", "-rq", ruleDir)
	git(t, dir, "commit", "-m", "delete the rule")
	deleted := git(t, dir, "rev-parse", "HEAD")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "match: '*.go'")
	assert.False(t, RuleAbsentFromLine(dir, deleted, ruleDir), "a rule deleted on the line was there: its deletion is a change")
}

func TestStandsUpstream(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "seed.txt", "s")
	tip := commitIn(t, dir, "a.txt", "tip version")
	tip = commitIn(t, dir, "b.txt", "tip version")
	git(t, dir, "branch", "up")
	git(t, dir, "checkout", "-q", "up")
	commitIn(t, dir, "a.txt", "rewritten since")
	up := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))

	assert.False(t, StandsUpstream(dir, tip, up, "a.txt", ""), "rewritten upstream since")
	assert.True(t, StandsUpstream(dir, tip, up, "b.txt", ""), "upstream holds it as the tip left it")
	assert.True(t, StandsUpstream(dir, tip, "", "a.txt", ""), "no upstream: never dropped")
	assert.True(t, StandsUpstream(dir, tip, up, "missing.txt", ""), "absent from both")
}
