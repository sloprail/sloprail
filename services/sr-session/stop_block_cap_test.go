package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

// A refused Stop is judged again on every retry; stop_hook_block_cap in the
// project's .sloprail/config.yaml is the only thing that lets a refusal loop end
// un-judged. The dispatch is stood in as REFUSING throughout, so every Stop that
// is judged blocks, and one that is not judged is the only way stdout stays empty.

func (s *session) setBlockCap(t *testing.T, yaml string) {
	t.Helper()
	cfg := filepath.Join(s.dir, ".sloprail", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(cfg), 0o755))
	require.NoError(t, os.WriteFile(cfg, []byte(yaml), 0o644))
}

// refusedStop ends a cycle with the dispatch refusing; retry says whether the
// harness sent it as a retry (stop_hook_active).
func (s *session) refusedStop(retry bool) (stdout, stderr string) {
	s.t.Helper()
	return s.withDispatch(false, func() (string, string) { return s.stop(retry) })
}

// The default: a retry is judged, not waved through — an agent does not pass a
// rule by replying twice. After DefaultStopHookBlockCap refusals in a row (the
// harness's own cap, 8), the next retry ends un-judged and says so.
// sr:proves session/turn-end-retry-judged-until-cap
func TestStopHookBlockCap_DefaultJudgesRetriesUpToTheHarnessCap(t *testing.T) {
	s := newSession(t)
	s.turn()

	out, _ := s.refusedStop(false)
	require.Contains(t, out, `"decision":"block"`, "the first Stop is refused")
	for i := 2; i <= declaration.DefaultStopHookBlockCap(); i++ {
		out, _ = s.refusedStop(true)
		require.Containsf(t, out, `"decision":"block"`,
			"retry %d was not judged — a Stop gate gave way to the agent replying again", i)
	}

	out, errOut := s.refusedStop(true)
	assert.Empty(t, out, "past the cap the retry must end rather than loop forever")
	assert.Contains(t, errOut, "stop_hook_block_cap (8)", "a turn let through with a rule still refusing must say so")
}

// 0 is no engine cap: the loop runs until a reply passes (or the harness's own
// cap ends it).
// sr:proves session/turn-end-retry-judged-until-cap
func TestStopHookBlockCap_ZeroNeverGivesWay(t *testing.T) {
	s := newSession(t)
	s.setBlockCap(t, "stop_hook_block_cap: 0\n")
	s.turn()

	s.refusedStop(false)
	for i := 0; i < 20; i++ {
		out, _ := s.refusedStop(true)
		require.Containsf(t, out, `"decision":"block"`, "retry %d gave way under a cap of 0", i+2)
	}
}

// 1 is the old engine's behaviour, now a project's explicit choice: refuse once,
// let the retry end un-judged — and leave the mark where it was.
// sr:proves session/turn-end-retry-judged-until-cap
func TestStopHookBlockCap_OneRefusesOnceThenLetsTheRetryEnd(t *testing.T) {
	s := newSession(t)
	s.setBlockCap(t, "stop_hook_block_cap: 1\n")
	s.turn()
	s.query()

	out, _ := s.refusedStop(false)
	require.Contains(t, out, `"decision":"block"`)

	out, _ = s.refusedStop(true)
	assert.Empty(t, out, "under a cap of 1 the retry ends un-judged")
	assert.Empty(t, s.mark(), "a Stop let through un-judged must not mark anything judged")
}

// A Stop that is not a retry begins a new sequence. A count left over from a
// sequence the harness cut short must not shorten the next turn's.
// sr:proves session/turn-end-retry-judged-until-cap
func TestStopHookBlockCap_ANewTurnStartsANewCount(t *testing.T) {
	s := newSession(t)
	s.setBlockCap(t, "stop_hook_block_cap: 2\n")
	s.turn()

	s.refusedStop(false) // 1
	s.refusedStop(true)  // 2 — the harness gives up here, say

	s.turn()
	out, _ := s.refusedStop(false)
	require.Contains(t, out, `"decision":"block"`)
	out, _ = s.refusedStop(true)
	assert.Contains(t, out, `"decision":"block"`,
		"the new turn inherited the previous turn's count and gave way after one refusal")
}

// A value the engine cannot use falls back to the default rather than to "no
// judging", and says why.
func TestStopHookBlockCap_ANegativeValueFallsBackToTheDefault(t *testing.T) {
	s := newSession(t)
	s.setBlockCap(t, "stop_hook_block_cap: -1\n")
	s.turn()

	s.refusedStop(false)
	out, errOut := s.refusedStop(true)
	assert.Contains(t, out, `"decision":"block"`, "an invalid cap must not switch judging off")
	assert.Contains(t, errOut, "stop_hook_block_cap")
}
