package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/transcript"
)

// A fork is a rollout of its own (its own thread id) whose session_meta names the thread it
// branched from (recorded: harness-mocks codex-mock session-fork). The conversation's
// identity crosses into the ancestor's rollout, found in the date-sharded sessions tree; with
// the ancestor gone the fork keeps its own root.
func TestForkResolvesToItsAncestorsIdentity(t *testing.T) {
	t.Setenv(harness.SelectEnv, "codex")
	const orig = "01a10378-c7e2-7c61-ba97-ab9d851d915c"
	const fork = "01a10378-ea00-7cd1-a8ba-bdd5221915cb"
	sessions := filepath.Join(t.TempDir(), "sessions")
	day := filepath.Join(sessions, "2026", "10", "03")
	require.NoError(t, os.MkdirAll(day, 0o755))
	write := func(id, ts, meta string) string {
		p := filepath.Join(day, "rollout-2026-10-03T"+ts+"-"+id+".jsonl")
		require.NoError(t, os.WriteFile(p, []byte(meta+"\n"), 0o644))
		return p
	}
	write(orig, "22-33-30",
		`{"timestamp":"2026-10-03T20:33:30.000Z","type":"session_meta","payload":{"id":"`+orig+`","session_id":"`+orig+`","forked_from_id":null}}`)
	forkPath := write(fork, "22-33-38",
		`{"timestamp":"2026-10-03T20:33:38.000Z","type":"session_meta","payload":{"id":"`+fork+`","session_id":"`+fork+`","forked_from_id":"`+orig+`"}}`)

	id, err := transcript.ResolveStableSessionID(sessions, forkPath)
	require.NoError(t, err)
	assert.Equal(t, orig, id.ID, "the fork continues the conversation of the thread it branched from")
	assert.NoError(t, id.Degraded)

	require.NoError(t, os.Remove(filepath.Join(day, "rollout-2026-10-03T22-33-30-"+orig+".jsonl")))
	id, err = transcript.ResolveStableSessionID(sessions, forkPath)
	require.NoError(t, err)
	assert.Equal(t, fork, id.ID, "with the ancestor gone the fork keeps its own root")
	assert.Error(t, id.Degraded)
}
