package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// T001_30: Stop runs the engine's real Stop path over the session's commits: the session's
// work is whatever the trajectory's run: steps commit after it begins, `checks_run` is the
// agent's `sr-checks run` (judges answered from the case), and Stop VERIFIES those verdicts,
// refusing while a stored fail stands.
func TestT001_30_StopVerifiesTheSessionsCommits(t *testing.T) {
	p := newNoTodo(t)
	kase(p, "file-guard", "no-todo", "stop-follows-the-commits", "description: the session commits a TODO, is told, fixes it\n",
		baseSetup, `- run: |
    mkdir docs
    echo 'TODO' > docs/a.md
    git add -A
    git commit -q -m "a doc"
- checks_run: {}
  expect: refuse
  reason_contains: still has a TODO
- kind: Stop
  expect: refuse
  reason_contains: docs/a.md
- run: |
    echo 'done' > docs/a.md
    git commit -q -am "finish the doc"
- checks_run: {}
  expect: permit
- kind: Stop
  expect: permit
`)
	res := p.Test("no-todo")
	require.Equal(t, 0, res.Code, res.Output)
	require.Contains(t, res.Output, "PASS stop-follows-the-commits")
}

// T001_31: uncommitted work on a path a file-guard selects is the Stop's "commit required", as
// in a real session: the engine's own order of Stop work, not a re-implementation of it.
func TestT001_31_StopRequiresTheWorkToBeCommitted(t *testing.T) {
	p := newNoTodo(t)
	kase(p, "file-guard", "no-todo", "uncommitted-doc", "description: x\n",
		baseSetup, `- run: |
    mkdir docs
    echo 'fine' > docs/a.md
- kind: Stop
  expect: refuse
  reason_contains: commit
- run: git add -A && git commit -q -m "a doc"
- kind: Stop
  expect: permit
`)
	res := p.Test("no-todo")
	require.Equal(t, 0, res.Code, res.Output)
}

// T001_32: a trajectory case may judge a file-guard rule's judge too, and the Stop it ends in
// reads what the judge stored.
func TestT001_32_AJudgeRuleThroughAStop(t *testing.T) {
	p := newPolite(t)
	kase(p, "file-guard", "polite", "a-rude-memo-keeps-the-stop-closed", "description: x\njudges:\n  judge.md.j2:\n    pass: false\n    reasoning: far too rude\n",
		baseSetup, `- run: |
    mkdir memos
    echo 'you are wrong' > memos/a.md
    git add -A
    git commit -q -m "a memo"
- checks_run: {}
  expect: refuse
  reason_contains: far too rude
- kind: Stop
  expect: refuse
  reason_contains: far too rude
`)
	res := p.Test("polite")
	require.Equal(t, 0, res.Code, res.Output)
}
