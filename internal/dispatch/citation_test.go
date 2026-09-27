package dispatch

import (
	"os"
	"path/filepath"
	"strings"
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
		"no citation after the fact":        {userOnly, citedEvent("PostFileUpdate"), true, "was changed without citing the user's own words (--cite:user)"},
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

// A `when` script that applies its prerequisite may say, on stdout, how to meet
// it in this case; the refusal carries that after the engine's own remedy.
func TestRequireWhenHint(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hint.sh"),
		[]byte("#!/bin/sh\necho '{\"hint\": \"Run the tests, then cite a line of their output.\"}'\nexit 0\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "prose.sh"),
		[]byte("#!/bin/sh\necho 'not json'\nexit 0\n"), 0o755))

	for script, want := range map[string]string{
		"./hint.sh":  "Run the tests, then cite a line of their output.",
		"./prose.sh": "",
	} {
		p := declaration.Prerequisite{
			Citation: &declaration.CitationPrerequisite{SourceTypes: []string{"tool_result"}},
			When:     script,
		}
		v, err := Runner{}.Run(Request{Nature: NatureFileGuard, Dir: dir, Require: []declaration.Prerequisite{p}, Event: citedEvent("PreFileUpdate")})
		require.NoError(t, err)
		require.True(t, v.Refused)
		assert.Contains(t, v.Reason, "must cite a tool's output from this session (--cite:tool_result)", "what to cite, always")
		assert.Contains(t, v.Reason, "ON ITS OWN", "how to run sr-file, always")
		if want != "" {
			// The hint is the how: it takes the generic forms' place.
			assert.Contains(t, v.Reason, "\n"+want+"\n")
			assert.NotContains(t, v.Reason, "sr-file delete", "a hint replaces the generic forms: %s", v.Reason)
		} else {
			assert.Contains(t, v.Reason, "sr-file delete", "without a hint the generic forms stay")
			assert.NotContains(t, v.Reason, "not json")
		}
	}
}

// A sub-agent refused for want of the user's words is told it never saw them:
// its prompt is the parent agent's.
func TestRequireCitationTellsASubagent(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "s.jsonl")
	require.NoError(t, os.WriteFile(root, []byte(`{"type":"user","uuid":"u1","parentUuid":null,"message":{"role":"user","content":"go"}}`+"\n"), 0o644))
	sub := filepath.Join(dir, "s", transcript.SubagentDir, "agent-abc.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sub), 0o755))
	require.NoError(t, os.WriteFile(sub, []byte(`{"type":"user","uuid":"s1","parentUuid":null,"isSidechain":true,"message":{"role":"user","content":"do it"}}`+"\n"), 0o644))

	userOnly := declaration.Prerequisite{Citation: &declaration.CitationPrerequisite{SourceTypes: []string{"user"}}}
	toolOnly := declaration.Prerequisite{Citation: &declaration.CitationPrerequisite{SourceTypes: []string{"tool_result"}}}
	for name, tc := range map[string]struct {
		transcript string
		req        declaration.Prerequisite
		told       bool
	}{
		"a sub-agent, the user pool":        {sub, userOnly, true},
		"the root, the user pool":           {root, userOnly, false},
		"a sub-agent, the tool_result pool": {sub, toolOnly, false},
	} {
		t.Run(name, func(t *testing.T) {
			v, err := Runner{}.Run(Request{Nature: NatureFileGuard, TranscriptPath: tc.transcript,
				Require: []declaration.Prerequisite{tc.req}, Event: citedEvent("PreFileUpdate")})
			require.NoError(t, err)
			require.True(t, v.Refused)
			assert.Equal(t, tc.told, strings.Contains(v.Reason, transcript.SubagentUserAdvice), "reason: %s", v.Reason)
		})
	}
}
