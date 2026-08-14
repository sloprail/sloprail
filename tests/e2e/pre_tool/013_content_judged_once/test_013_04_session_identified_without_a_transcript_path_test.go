package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingRail counts every creation it is asked about and permits it. Named to
// sort BEFORE the blocker below, so it is asked before the refusal
// short-circuits the event.
const countingRail = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./count.sh
---

# counts every creation it is asked about
`

// blockingRail refuses every write, so nothing ever lands.
const blockingRail = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./no.sh
---

# refuses every write, so nothing lands
`

func TestT013_04_TheSessionIsIdentifiedWithoutATranscriptPath(t *testing.T) {
	// What makes every test in this package reachable, pinned so it cannot
	// quietly go away.
	//
	// The mock's main-session PreToolUse payload really does omit
	// transcript_path — it carries session_id and cwd and nothing else. This
	// branch previously read that fact as "the dispatch is unreachable end to
	// end" and deleted its e2e over it. The fact was right and the conclusion was
	// wrong: identity is read from the ORIGIN RECORD inside the transcript, so
	// locating the file by the harness's own naming resolves the same
	// conversation a handed-over path would. Only the file NAME is assumed.
	//
	// Non-vacuous, and it took two attempts to make it so. The obvious scenario —
	// write the same bytes to the same path twice — proves nothing: the first
	// write LANDS, so the second offer is an update, a kind that is never exempt
	// by design, and the count is 1 whether the session resolved or not.
	//
	// So the creation is made to repeat. A second rule refuses every write, the
	// file never lands, and the SAME pending creation is offered twice. Now the
	// count separates the two worlds — 1 with the record, 2 without — which was
	// checked both ways by disabling the fallback.
	e := New(t)
	proj := e.Project()

	e.Guardrail(proj, "a-counter", countingRail, map[string]string{
		"count.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> ./ran.log\nexit 0\n",
	})
	e.Guardrail(proj, "z-blocker", blockingRail, map[string]string{
		"no.sh": "#!/bin/sh\ncat >/dev/null\necho 'not today' >&2\nexit 1\n",
	})

	res := e.Run(proj, "sess-013-04", "offer the same creation twice", Turns("done",
		Write("t1", "a.md", "same bytes"),
		Write("t2", "a.md", "same bytes"),
	))
	require.False(t, res.Saw("session state unavailable"),
		"the session must be identifiable from a payload carrying no transcript path")

	assert.Equal(t, 1, len(e.Ledger(proj, "a-counter", "ran.log")),
		"the same pending creation offered twice reaches the rule once — which it can only do if the session was identified from a payload with no transcript path")
}
