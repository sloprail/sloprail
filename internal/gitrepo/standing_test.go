package gitrepo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ruleDir = ".sloprail/file-guard/docs"

func TestRaiseBaseToRuleFloor(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "seed.txt", "s")
	commitIn(t, dir, "before.txt", "work before the rule")
	beforeRule := git(t, dir, "rev-parse", "HEAD")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "match: '*.go'")
	commitIn(t, dir, "after.txt", "work after the rule")
	head := git(t, dir, "rev-parse", "HEAD")

	r, err := RaiseBaseToRuleFloor(dir, Range{Base: base, Head: head}, ruleDir)
	require.NoError(t, err)
	assert.Equal(t, beforeRule, r.Base, "the floor is the parent of the rule's add commit")
	assert.Equal(t, head, r.Head)

	r, err = RaiseBaseToRuleFloor(dir, Range{Base: head, Head: head}, ruleDir)
	require.NoError(t, err)
	assert.Equal(t, head, r.Base, "an empty range stays empty")

	later := commitIn(t, dir, "later.txt", "x")
	r, err = RaiseBaseToRuleFloor(dir, Range{Base: head, Head: later}, ruleDir)
	require.NoError(t, err)
	assert.Equal(t, head, r.Base, "a base after the floor is never moved earlier")

	r, err = RaiseBaseToRuleFloor(dir, Range{Base: base, Head: later}, ".sloprail/file-guard/other")
	require.NoError(t, err)
	assert.Equal(t, base, r.Base, "a rule committed nowhere has no floor")
	r, err = RaiseBaseToRuleFloor(dir, Range{Base: base, Head: later}, "")
	require.NoError(t, err)
	assert.Equal(t, base, r.Base, "a rule outside the repository has no floor")
}

func TestRaiseBaseToRuleFloor_FromTheEmptyTreeAndLastChange(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, "seed.txt", "s")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	beforeEdit := commitIn(t, dir, "x.txt", "x")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v2")
	head := commitIn(t, dir, "y.txt", "y")

	r, err := RaiseBaseToRuleFloor(dir, Range{Base: EmptyTree, Head: head}, ruleDir)
	require.NoError(t, err)
	assert.Equal(t, beforeEdit, r.Base, "the rule's LAST change sets the floor")
}

func TestRaiseBaseToRuleFloor_ARuleStandingAtTheBaseStaysStrict(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	base := commitIn(t, dir, "seed.txt", "s")
	commitIn(t, dir, "bad.txt", "work under the rule")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v2")
	head := commitIn(t, dir, "y.txt", "y")

	r, err := RaiseBaseToRuleFloor(dir, Range{Base: base, Head: head}, ruleDir)
	require.NoError(t, err)
	assert.Equal(t, base, r.Base, "a rule that stood at the base is not raised by a later edit")
}
