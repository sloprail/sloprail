package hookinput

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/internal/harness"
)

// locating wraps whatever harness is registered and adds a TranscriptLocator.
type locating struct{ harness.Harness }

func (locating) LocateTranscript(in harness.HookInput) string { return "/found/" + in.SessionID + ".jsonl" }

func TestSessionRecord_AHarnessLocatorFindsARecordThePayloadDidNotName(t *testing.T) {
	harness.Register(locating{harness.Current()})
	t.Cleanup(func() { harness.Register(harness.Current().(locating).Harness) })

	got, err := SessionRecord(harness.HookInput{SessionID: "s1", Cwd: "/proj"})
	assert.NoError(t, err)
	assert.Equal(t, "/found/s1.jsonl", got)

	got, err = SessionRecord(harness.HookInput{TranscriptPath: "/given.jsonl", SessionID: "s1"})
	assert.NoError(t, err)
	assert.Equal(t, "/given.jsonl", got, "a path the payload names is never second-guessed")
}
