package ambientenv

import (
	"slices"
	"testing"
)

var ambient = []string{
	"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=x", "CLAUDE_CODE_ENTRYPOINT=cli",
	"CLAUDE_CODE_EXECPATH=/bin/claude", "CLAUDE_CODE_SSE_PORT=1", "CLAUDE_PROJECT_DIR=/p",
	"CLAUDE_PLUGIN_ROOT=/r", "CLAUDE_CONFIG_DIR=/c", "SLOPRAIL_LAUNCHED_BY=a",
	"SLOPRAIL_X=1", "SR_WORKSPACE=/w", "SLOP_SUBBIN_DIR=/b",
	"CLAUDE_CODE_OAUTH_TOKEN=t", "CLAUDE_CODE_CLIENT_CERT=c", "ANTHROPIC_API_KEY=k", "PATH=/usr/bin", "HOME=/h",
}

func TestSessionDropsIdentityKeepsAuthAndRest(t *testing.T) {
	got := Session(ambient)
	want := []string{"CLAUDE_CODE_OAUTH_TOKEN=t", "CLAUDE_CODE_CLIENT_CERT=c", "ANTHROPIC_API_KEY=k", "PATH=/usr/bin", "HOME=/h",
		"CLAUDE_CONFIG_DIR=/c", "SLOPRAIL_X=1", "SR_WORKSPACE=/w", "SLOP_SUBBIN_DIR=/b"}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestHermeticAlsoDropsSloprailAndConfig(t *testing.T) {
	got := Hermetic(ambient)
	want := []string{"CLAUDE_CODE_OAUTH_TOKEN=t", "CLAUDE_CODE_CLIENT_CERT=c", "ANTHROPIC_API_KEY=k", "PATH=/usr/bin", "HOME=/h"}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}
