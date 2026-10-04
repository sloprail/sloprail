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

// The event says where the check ran: the caller's On, "Stop" when it names none.
func TestEmitFileGuardEventsNamesWhereItRan(t *testing.T) {
	f := filepath.Join(t.TempDir(), "ev.jsonl")
	t.Setenv("SR_EVENTS_FILE", f)
	out := []*ruleRun{{g: declaration.FileGuard{Name: "r", Origin: declaration.Origin{Plugin: "p"}}, settled: true}}
	emitFileGuardEvents(out, "sr-checks verify", false)
	emitFileGuardEvents(out, "", false)
	b, err := os.ReadFile(f)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], `"rule":"p/r","outcome":"passed"`)
	assert.Contains(t, lines[0], `"on":"sr-checks verify"`)
	assert.Contains(t, lines[1], `"on":"Stop"`)
}

// At the Stop a key with no stored verdict is dropped, not a refusal: it is not logged as one. Anywhere
// else (sr-checks verify) it is a refusal, and a stored fail is one at the Stop too.
func TestEmitFileGuardEventsSkipsUnjudgedAtStop(t *testing.T) {
	f := filepath.Join(t.TempDir(), "ev.jsonl")
	t.Setenv("SR_EVENTS_FILE", f)
	g := declaration.FileGuard{Name: "r"}
	unjudged := []*ruleRun{{g: g, settled: true, refused: true, result: FileGuardResult{Reason: "not judged yet — run `sr-checks run`"}}}
	failed := []*ruleRun{{g: g, settled: true, refused: true, result: FileGuardResult{Reason: "a real refusal"}}}
	emitFileGuardEvents(unjudged, "", true)
	_, err := os.Stat(f)
	assert.True(t, os.IsNotExist(err), "an unjudged key at the Stop logged an event")
	emitFileGuardEvents(unjudged, "sr-checks verify", false)
	emitFileGuardEvents(failed, "", true)
	b, err := os.ReadFile(f)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], `"outcome":"refused"`)
	assert.Contains(t, lines[1], "a real refusal")
}
