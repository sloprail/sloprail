package claudecode

import (
	"path/filepath"
	"testing"
)

// The operator's login is in Claude Code's config directory as Claude Code resolves it:
// $CLAUDE_CONFIG_DIR when set, else ~/.claude. The agent's place for it is always the
// default one under the sandbox's HOME.
func TestAuthFiles_ResolveTheConfigDirLikeClaudeCode(t *testing.T) {
	cases := []struct {
		name, configDir, want string
	}{
		{"unset", "", filepath.Join("/home/op", ".claude", ".credentials.json")},
		{"blank", "  ", filepath.Join("/home/op", ".claude", ".credentials.json")},
		{"set", "/home/op/.claude-accounts/work", filepath.Join("/home/op/.claude-accounts/work", ".credentials.json")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := Harness{}.AuthFiles("/home/op", func(k string) string {
				if k == "CLAUDE_CONFIG_DIR" {
					return c.configDir
				}
				return ""
			})
			if len(files) != 1 {
				t.Fatalf("want one login file, got %v", files)
			}
			if files[0].Real != c.want {
				t.Errorf("operator's login %q, want %q", files[0].Real, c.want)
			}
			if want := filepath.Join(".claude", ".credentials.json"); files[0].InHome != want {
				t.Errorf("the agent's login at ~/%s, want ~/%s", files[0].InHome, want)
			}
		})
	}
}
