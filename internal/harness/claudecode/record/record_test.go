package record

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseEntry(t *testing.T, line string) (refused bool, reasons []string, has bool) {
	t.Helper()
	r, err := Transcripts{}.ParseRecord([]byte(line))
	require.NoError(t, err)
	e := r.Entry()
	if e.StopHook == nil {
		return false, nil, false
	}
	return e.StopHook.Refused, e.StopHook.Reasons, true
}

func TestStopHookSummaryOutcome(t *testing.T) {
	refused, _, has := parseEntry(t, `{"type":"system","subtype":"stop_hook_summary","uuid":"u1","hookErrors":[],"preventedContinuation":false}`)
	assert.True(t, has)
	assert.False(t, refused, "a clean summary is a pass")

	refused, reasons, has := parseEntry(t, `{"type":"system","subtype":"stop_hook_summary","uuid":"u2","hookErrors":["gate \"x\" failed"],"preventedContinuation":false}`)
	assert.True(t, has)
	assert.True(t, refused)
	assert.Equal(t, []string{`gate "x" failed`}, reasons)
}

func TestOtherRecordsCarryNoStopHook(t *testing.T) {
	_, _, has := parseEntry(t, `{"type":"system","subtype":"turn_duration","uuid":"u4"}`)
	assert.False(t, has)
	_, _, has = parseEntry(t, `{"type":"user","uuid":"u5","message":{"role":"user","content":"hi"}}`)
	assert.False(t, has)
}
