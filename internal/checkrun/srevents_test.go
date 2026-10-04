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
	emitFileGuardEvents(out, "sr-checks verify")
	emitFileGuardEvents(out, "")
	b, err := os.ReadFile(f)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], `"rule":"p/r","outcome":"passed"`)
	assert.Contains(t, lines[0], `"on":"sr-checks verify"`)
	assert.Contains(t, lines[1], `"on":"Stop"`)
}
