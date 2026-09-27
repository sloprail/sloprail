package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/internal/transcript"
)

// A sub-agent's command whose --cite:user quote cannot resolve is refused up
// front, with what sr-file or cite would say and what a sub-agent needs to hear
// — and nothing else is: not the root's calls, not a command that would have
// done something before the failing call, not a quote that resolves.
func TestSubagentUserCitationRefusal(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	tree := transcript.ResolveWorkDir(t.TempDir())
	root, _ := citedSubagentSession(t, cfg, tree, "sess-1", "abc")

	bash := func(command string, sub bool) HookPayload {
		in, _ := json.Marshal(map[string]string{"command": command})
		p := HookPayload{SessionID: "sess-1", Cwd: tree, ToolName: "Bash", ToolInput: in}
		if sub {
			p.AgentID = "abc"
		} else {
			p.TranscriptPath = root
		}
		return p
	}

	for name, tc := range map[string]struct {
		command string
		sub     bool
		says    []string
	}{
		"the dispatch prompt as the user": {`sr-file write a.md --cite:user 'do it' --content x`, true,
			[]string{"sr-file write: nothing written", "That quote is from your dispatch prompt, written by the parent agent.", transcript.SubagentUserAdvice}},
		"words nobody said": {`sr-file write a.md --cite:user 'nobody said this' --content x`, true,
			[]string{"does not resolve", transcript.SubagentUserAdvice}},
		"a cite chain": {`sr-session trajectory cite 'nobody said this' && touch x`, true,
			[]string{"sr-session trajectory cite:", transcript.SubagentUserAdvice}},
		"glue before it": {`echo go && sr-file delete a.md --cite:user 'nobody said this'`, true,
			[]string{transcript.SubagentUserAdvice}},

		"the user's own words resolve":      {`sr-file write a.md --cite:user 'go' --content x`, true, nil},
		"the root's call":                   {`sr-file write a.md --cite:user 'nobody said this' --content x`, false, nil},
		"a tool_result quote":               {`sr-file write a.md --cite:tool_result 'nobody said this' --content x`, true, nil},
		"an effect before the failing call": {`touch y && sr-file write a.md --cite:user 'nobody said this' --content x`, true, nil},
		"an sr-file write before it":        {`sr-file write b.md --cite:user 'go' --content x && sr-file write a.md --cite:user 'nobody said this' --content x`, true, nil},
		"not one && chain":                  {`sr-file write a.md --cite:user 'nobody said this' --content x; echo done`, true, nil},
		"an expansion":                      {`sr-file write a.md --cite:user "$Q" --content x`, true, nil},
		"cite pointed elsewhere":            {`sr-session trajectory cite --path /elsewhere.jsonl 'nobody said this' && touch x`, true, nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := subagentUserCitationRefusal(bash(tc.command, tc.sub))
			if tc.says == nil {
				assert.Empty(t, got)
				return
			}
			for _, want := range tc.says {
				assert.Contains(t, got, want)
			}
		})
	}
}
