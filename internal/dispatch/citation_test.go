package dispatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/transcript"
)

func citedEvent(kind string, pools ...transcript.SourceType) event.Event {
	var cs []transcript.Citation
	if len(pools) > 0 {
		cs = []transcript.Citation{{Quote: "q", SourceTypes: pools, Path: "/s.jsonl", Line: 3}}
	}
	return event.Event{Kind: kind, Fields: map[string]any{
		"path":                   "memories/a.md",
		grounding.FieldCitations: grounding.ToWire(cs),
	}}
}

func TestRequireCitation(t *testing.T) {
	userOnly := declaration.Prerequisite{Citation: &declaration.CitationPrerequisite{}}
	toolOnly := declaration.Prerequisite{Citation: &declaration.CitationPrerequisite{SourceTypes: []string{"tool_result"}}}

	for name, tc := range map[string]struct {
		req    declaration.Prerequisite
		ev     event.Event
		refuse bool
		says   string
	}{
		"user citation meets the default":   {userOnly, citedEvent("PreFileUpdate", transcript.SourceUser), false, ""},
		"no citation on a file change":      {userOnly, citedEvent("PreFileUpdate"), true, "sr-file edit memories/a.md"},
		"no citation on a command":          {userOnly, citedEvent("PreCommandInvoke"), true, "sr-session trajectory cite"},
		"no citation after the fact":        {userOnly, citedEvent("PostFileUpdate"), true, "was changed without a citation"},
		"user citation, tool_result wanted": {toolOnly, citedEvent("PreCommandInvoke", transcript.SourceUser), true, "tool_result"},
		"tool_result citation meets it":     {toolOnly, citedEvent("PreCommandInvoke", transcript.SourceToolResult), false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			v, err := Runner{}.Run(Request{Nature: NatureGate, Require: []declaration.Prerequisite{tc.req}, Event: tc.ev})
			require.NoError(t, err)
			assert.Equal(t, tc.refuse, v.Refused, "reason: %s", v.Reason)
			if tc.says != "" {
				assert.Contains(t, v.Reason, tc.says)
			}
		})
	}
}
