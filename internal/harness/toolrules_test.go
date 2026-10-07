package harness

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseToolRule(t *testing.T) {
	for in, want := range map[string]ToolRule{
		"Read":                  {Raw: "Read", Name: "Read"},
		"WebFetch":              {Raw: "WebFetch", Name: "WebFetch"},
		"Bash":                  {Raw: "Bash", Name: "Bash"},
		"Bash(ls:*)":            {Raw: "Bash(ls:*)", Name: "Bash", Prefix: "ls"},
		"Bash(curl -sL x)":      {Raw: "Bash(curl -sL x)", Name: "Bash", Scoped: true},
		"Edit(//tmp/x/**)":      {Raw: "Edit(//tmp/x/**)", Name: "Edit", Scoped: true},
		"WebFetch(domain:a.io)": {Raw: "WebFetch(domain:a.io)", Name: "WebFetch", Scoped: true},
		"mcp__srv__tool":        {Raw: "mcp__srv__tool", Name: "mcp__srv__tool"},
		"  Grep ":               {Raw: "Grep", Name: "Grep"},
	} {
		got, err := ParseToolRule(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "  ", "Bash(ls:*"} {
		_, err := ParseToolRule(bad)
		assert.Error(t, err, "%q", bad)
	}
}
