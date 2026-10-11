package codex

import (
	"path/filepath"
	"testing"
)

// The operator's Codex login is auth.json in $CODEX_HOME when set, else ~/.codex.
func TestAuthFiles_FollowCodexHome(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "CODEX_HOME" {
				return v
			}
			return ""
		}
	}
	if got := (Harness{}).AuthFiles("/home/op", env(""))[0].Real; got != filepath.Join("/home/op", ".codex", "auth.json") {
		t.Errorf("default: %q", got)
	}
	f := (Harness{}).AuthFiles("/home/op", env("/srv/codex"))[0]
	if f.Real != filepath.Join("/srv/codex", "auth.json") || f.InHome != filepath.Join(".codex", "auth.json") {
		t.Errorf("CODEX_HOME: %+v", f)
	}
}
