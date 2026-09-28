package e2e

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Every guard script this plugin ships parses under bash — the shell the hooks
// run them with. A script that does not parse can fail open: a sourced helper
// that stops at a syntax error defines nothing, and the `.` that loaded it does
// not fail. (An apostrophe inside a double-quoted ${var:-…} is valid zsh and a
// bash syntax error; that one shipped for a moment.)
func TestGuardScriptsParseUnderBash(t *testing.T) {
	scripts, err := filepath.Glob(filepath.Join(pluginRoot(t), ".sloprail", "*", "*", "*.sh"))
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no guard scripts found (%v)", err)
	}
	for _, s := range scripts {
		if out, err := exec.Command("bash", "-n", s).CombinedOutput(); err != nil {
			t.Errorf("%s does not parse under bash: %v\n%s", s, err, out)
		}
	}
}
