package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// order_within_binding, at the Post hook point.
//
// "Within one Binding, Hooks run in the order they are declared." The Pre side
// has an e2e directory for this (010_order_within_binding); the Post side had
// nothing, and the property is not free — it holds only because runHooks walks
// b.Hooks in slice order and the YAML decoder preserves sequence order.
//
// A rule may sequence its own hooks, one writing what the next reads, and the
// list it wrote them in is the only order it can express. Nothing else about
// dispatch is ordered — between guardrails no order is promised, and the map
// this dispatch walks would not give one anyway.

// appendMark writes one line naming itself, so a ledger reads as a sequence.
const appendMark = `#!/bin/sh
echo "$MARK" >> "$SLOPRAIL_TEST_LEDGER/order.txt"
`

// TestDispatch_HooksRunInDeclaredOrderAtPost.
//
// Three hooks in one binding, each writing its own name. The declared order is
// first, second, third; anything else means a rule cannot sequence its own
// work.
func TestDispatch_HooksRunInDeclaredOrderAtPost(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	ldir := ledgerDir(t)
	const ordered = `---
hooks:
  TurnEnd:
    - hooks:
        - type: command
          command: MARK=first ./mark.sh
        - type: command
          command: MARK=second ./mark.sh
        - type: command
          command: MARK=third ./mark.sh
---

# Three hooks, one binding
`
	guardrailDir(t, proj, "sequenced", ordered, map[string]string{"mark.sh": appendMark})
	store := openStore(t)
	baselineAt(t, store, proj)

	_, ran := dispatchIn(t, proj, store)
	require.True(t, ran)

	assert.Equal(t, []string{"first", "second", "third"}, ledger(t, ldir, "order.txt"),
		"hooks in one binding run in the order they are declared")
}

// TestDispatch_HooksAfterARefusalDoNotRunAtPost.
//
// runHooks stops at the first refusal, which is what makes a binding's hooks a
// sequence rather than a set. The second hook here would append if it ran, so
// its absence is the claim.
//
// Worth pinning separately from the order above: a change that gathered every
// hook's verdict before deciding would keep the ORDER and lose this, and the
// ledger is the only place the difference shows.
func TestDispatch_HooksAfterARefusalDoNotRunAtPost(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	ldir := ledgerDir(t)
	const refuseThenMark = `---
hooks:
  TurnEnd:
    - hooks:
        - type: command
          command: MARK=before ./mark.sh
        - type: command
          command: ./refuse.sh
        - type: command
          command: MARK=after ./mark.sh
---

# A refusal in the middle
`
	guardrailDir(t, proj, "stops", refuseThenMark, map[string]string{
		"mark.sh":   appendMark,
		"refuse.sh": alwaysRefuse,
	})
	store := openStore(t)
	baselineAt(t, store, proj)

	stdout, ran := dispatchOut(t, proj, store)

	assert.False(t, ran, "a refusal holds the turn")
	assert.Contains(t, stdout, `"decision":"block"`)
	assert.Equal(t, []string{"before"}, ledger(t, ldir, "order.txt"),
		"the hooks after a refusing one do not run")
}

// TestDispatch_EveryGuardrailIsHeardBeforeTheTurnIsHeld.
//
// The other half, and the one that is NOT ordering: one guardrail refusing must
// not cost the others their run. dispatchAll collects rather than returning
// early precisely so the agent is told everything at once — being handed one
// violation per turn is the slow version of the same bug.
//
// Two guardrails, both refusing, both named in the single block.
func TestDispatch_EveryGuardrailIsHeardBeforeTheTurnIsHeld(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	const refuseTurnEnd = `---
hooks:
  TurnEnd:
    - hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses the cycle
`
	guardrailDir(t, proj, "alpha", refuseTurnEnd, map[string]string{"refuse.sh": alwaysRefuse})
	guardrailDir(t, proj, "omega", refuseTurnEnd, map[string]string{"refuse.sh": alwaysRefuse})
	store := openStore(t)
	baselineAt(t, store, proj)

	stdout, ran := dispatchOut(t, proj, store)

	assert.False(t, ran)
	assert.Contains(t, stdout, "alpha", "the first refusal must be reported")
	assert.Contains(t, stdout, "omega",
		"a refusal by one guardrail must not silence the rules dispatched after it")
}
