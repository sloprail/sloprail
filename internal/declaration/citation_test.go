package declaration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/transcript"
)

func TestLoad_CitationPrerequisiteShapes(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/bare/file-guard.yaml": `
match: "**/*.md"
require:
  - citation: {source_types: [user]}
`,
		"gate/pools/gate.yaml": `
on:
  - event: PreCommandInvoke
require:
  - citation:
      source_types: [user, tool_result]
`,
	})
	require.Len(t, loaded.FileGuards, 1)
	require.Len(t, loaded.Gates, 1)
	bare := loaded.FileGuards[0].Require[0].Citation
	require.NotNil(t, bare)
	assert.Equal(t, []transcript.SourceType{transcript.SourceUser}, bare.Pools())
	pools := loaded.Gates[0].Require[0].Citation
	require.NotNil(t, pools)
	assert.Equal(t, []transcript.SourceType{transcript.SourceUser, transcript.SourceToolResult}, pools.Pools())
}

func TestLoad_CitationPrerequisiteRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, kind, says string }{
		"unknown pool": {`
on:
  - event: PreCommandInvoke
require:
  - citation:
      source_types: [assistant]
`, "", "assistant"},
		"on Stop": {`
on:
  - event: Stop
require:
  - citation: {source_types: [user]}
`, "", "Stop carries no citations"},
		"with skill": {`
on:
  - event: PreCommandInvoke
require:
  - citation: {source_types: [user]}
    skill: x
`, "", "exactly one of skill, context or citation"},
		"no pools": {`
on:
  - event: PreCommandInvoke
require:
  - citation: {}
`, "", "names no source_types"},
	} {
		t.Run(name, func(t *testing.T) {
			iv := loadOneInvalid(t, map[string]string{"gate/g/gate.yaml": tc.yaml})
			assert.Contains(t, iv.Reason, tc.says)
		})
	}
}

func TestLoad_CitationPrerequisiteYAMLRefusals(t *testing.T) {
	for name, body := range map[string]string{
		"true":        "citation: true",
		"false":       "citation: false",
		"typo key":    "citation:\n      sourcetypes: [user]",
		"scalar junk": "citation: yes-please",
	} {
		t.Run(name, func(t *testing.T) {
			iv := loadOneInvalid(t, map[string]string{"gate/g/gate.yaml": "on:\n  - event: PreCommandInvoke\nrequire:\n  - " + body + "\n"})
			assert.Contains(t, iv.Reason, "citation")
		})
	}
}
