package dispatch

import (
	"os"
	"path/filepath"
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
	userOnly := declaration.Prerequisite{Citation: &declaration.CitationPrerequisite{SourceTypes: []string{"user"}}}
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

// A prerequisite's `when` script decides whether it applies: only exit 1
// waives it; exit 0, any other code, or a script that cannot run applies it.
func TestRequireWhen(t *testing.T) {
	dir := t.TempDir()
	writeScript := func(name, body string) string {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755))
		return "./" + name
	}
	for name, tc := range map[string]struct {
		when   string
		refuse bool
	}{
		"exit 0 applies it":            {writeScript("applies.sh", "exit 0"), true},
		"exit 1 waives it":             {writeScript("waives.sh", "exit 1"), false},
		"another code applies it":      {writeScript("broken.sh", "exit 2"), true},
		"a missing script applies it":  {"./missing.sh", true},
		"it reads the payload":         {writeScript("reads.sh", `jq -e '.event.kind == "PreFileUpdate"' >/dev/null || exit 1`), true},
		"it reads the payload, waives": {writeScript("reads-no.sh", `jq -e '.event.kind == "PreFileCreate"' >/dev/null || exit 1`), false},
	} {
		t.Run(name, func(t *testing.T) {
			p := declaration.Prerequisite{
				Citation: &declaration.CitationPrerequisite{SourceTypes: []string{"user"}},
				When:     tc.when,
			}
			v, err := Runner{}.Run(Request{Nature: NatureFileGuard, Dir: dir, Require: []declaration.Prerequisite{p}, Event: citedEvent("PreFileUpdate")})
			require.NoError(t, err)
			assert.Equal(t, tc.refuse, v.Refused, "reason: %s", v.Reason)
		})
	}
}
