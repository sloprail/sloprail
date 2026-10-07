package codex

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
)

func mapRules(t *testing.T, allow, deny []string, writable int) ([]string, error) {
	t.Helper()
	a, err := harness.ParseToolRules(allow)
	require.NoError(t, err)
	d, err := harness.ParseToolRules(deny)
	require.NoError(t, err)
	return Harness{}.MapToolRules(a, d, harness.ToolContext{WritableDirs: writable})
}

func TestMapToolRules_NothingAllowedSwitchesEverythingOff(t *testing.T) {
	got, err := mapRules(t, nil, nil, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"-c", "sandbox_workspace_write.network_access=false",
		"-c", `web_search="disabled"`,
		"--disable", "multi_agent",
	}, got, "web_search defaults to cached and multi_agent to on: a judge granted nothing must not get them")
}

func TestMapToolRules_EachCapabilityOpensItsOwnSwitch(t *testing.T) {
	got, err := mapRules(t, []string{"Read", "Grep", "Glob", "Bash", "Write", "Edit", "WebFetch", "WebSearch", "Agent"}, nil, 1)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"-c", "sandbox_workspace_write.network_access=true",
		"-c", `web_search="live"`,
	}, got)
}

func TestMapToolRules_RefusesWhatItCannotExpress(t *testing.T) {
	for name, tc := range map[string]struct {
		allow, deny []string
		writable    int
	}{
		"bash prefix":            {allow: []string{"Bash(ls:*)"}, writable: 1},
		"path scope":             {allow: []string{"Read(//x/**)"}, writable: 1},
		"mcp tool":               {allow: []string{"mcp__s__t"}, writable: 1},
		"unknown tool":           {allow: []string{"Teleport"}, writable: 1},
		"write with nowhere":     {allow: []string{"Edit"}},
		"fetch without sandbox":  {allow: []string{"WebFetch"}},
		"deny the shell":         {deny: []string{"Bash"}, writable: 1},
		"deny a file tool":       {deny: []string{"Write"}, writable: 1},
		"deny a scoped rule":     {deny: []string{"WebFetch(domain:x)"}, writable: 1},
		"allow and deny search":  {allow: []string{"WebSearch"}, deny: []string{"WebSearch"}, writable: 1},
		"allow and deny a fetch": {allow: []string{"WebFetch"}, deny: []string{"WebFetch"}, writable: 1},
	} {
		_, err := mapRules(t, tc.allow, tc.deny, tc.writable)
		assert.ErrorIs(t, err, harness.ErrToolUnsupported, name)
	}
}

func TestMapToolRules_DenyingWhatIsAlreadyOffIsFine(t *testing.T) {
	_, err := mapRules(t, nil, []string{"WebFetch", "WebSearch", "Agent", "mcp__s__t"}, 0)
	assert.NoError(t, err)
}
