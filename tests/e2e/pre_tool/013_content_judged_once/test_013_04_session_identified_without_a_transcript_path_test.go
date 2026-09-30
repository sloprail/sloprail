package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T013_04: the session is identified without a transcript path — which is what
// makes every test in this package reachable, pinned so it cannot quietly go away.
//
// The mock's main-session payload really does omit transcript_path — it carries
// session_id and cwd and nothing else. Identity is read from the ORIGIN RECORD
// inside the transcript, so locating the file by the harness's own naming resolves
// the same conversation a handed-over path would. Only the file NAME is assumed.
//
// # How this is proven for the file-guard nature
//
// The check results that move a rule's base are keyed to the SESSION. If the session
// could not be identified each cycle, a fresh store would be used every time and no
// pass could ever be found — so a rule that already passed would judge the same
// range again. The base staying put is therefore proof the session was resolved from
// a payload with no transcript path: the pass recorded in the first cycle was found
// in the second, which can only happen if both cycles resolved the same session.
//
// Non-vacuous by construction. If identity did not resolve, the second cycle would
// re-judge the identical range (a fresh store, no recorded pass), and the count would
// climb — which is exactly what the assertion below forbids.
func TestT013_04_TheSessionIsIdentifiedWithoutATranscriptPath(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	judgeRail(e, proj)

	sess := "sess-013-04"

	// Cycle 1: a benign file, judged and passed. The pass is recorded in the
	// session-keyed check results.
	res := e.Run(proj, sess, "write a benign memory", Turns("done",
		Write("t1", "memories/note.md", "same bytes"),
	).ThenCommit("add the memory"))
	require.False(t, res.Saw("session state unavailable"),
		"the session must be identifiable from a payload carrying no transcript path")
	require.False(t, res.Saw("no session"),
		"the session must be identifiable from a payload carrying no transcript path")
	first := asks(e, proj)
	require.Greater(t, first, 0, "the benign file must be judged in the first cycle")

	// Cycle 2: the SAME content again, so nothing new is committed. Not judged again
	// — which requires finding the first cycle's pass in the same session's results,
	// so the session resolved from a transcript-path-less payload both times.
	e.Run(proj, sess, "offer the same content again", Turns("done",
		Write("t2", "memories/note.md", "same bytes"),
	))
	after := asks(e, proj)
	assert.Equal(t, first, after,
		"the same content offered again was judged again — the session was not identified from a payload with no transcript path, so the recorded pass could not be found")
}
