package e2e

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestSrTestNestedSloprailDirs: `sr-test run` finds cases in .sloprail/ folders below the root, a
// plugin's case runs with that plugin installed and without the host's .sloprail/, same-named cases
// do not collide, and a harness with no released mock is an error whose message reaches the output.
func TestSrTestNestedSloprailDirs(t *testing.T) {
	e := New(t)
	root := t.TempDir()
	write(t, root, ".sloprail/tests/same/test.sh", `test -f .sloprail/tests/same/test.sh && test -z "$SR_TEST_PLUGIN_DIR"`)
	write(t, root, "marketplace/plugins/p/.claude-plugin/plugin.json", `{"name":"p"}`)
	write(t, root, "marketplace/plugins/p/.sloprail/tests/same/test.sh", `test ! -e .sloprail && case "$SR_TEST_PLUGIN_DIR" in *plugins/p*) ;; *) exit 1;; esac`)
	write(t, root, "marketplace/plugins/p/.sloprail/tests/codex/test.sh",
		`out=$(sr-test agent nothing.sh --harness codex 2>&1); code=$?; echo "$out"; test $code = 2 && exit 2; exit 1`)
	write(t, root, "node_modules/x/.sloprail/tests/skipped/test.sh", "exit 1")

	res := e.CLIDirect(root, "sr-test", "run")
	got := map[string]struct{ Status, Output string }{}
	sc := bufio.NewScanner(strings.NewReader(res.Output))
	for sc.Scan() {
		var r struct{ Subject, Status, Output string }
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Subject != "" {
			got[r.Subject] = struct{ Status, Output string }{r.Status, r.Output}
		}
	}
	if len(got) != 3 {
		t.Fatalf("want 3 cases, got %v\n%s", got, res.Output)
	}
	if got["same"].Status != "pass" || got["marketplace/plugins/p:same"].Status != "pass" {
		t.Errorf("same-name cases: %+v", got)
	}
	c := got["marketplace/plugins/p:codex"]
	if c.Status != "error" || !strings.Contains(c.Output, "codex-mock not released yet") {
		t.Errorf("codex case: %+v", c)
	}
}
