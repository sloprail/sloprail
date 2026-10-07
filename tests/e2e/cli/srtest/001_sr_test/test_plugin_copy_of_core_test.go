package e2e

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"
)

// TestSrTestPluginCopyOfCoreIsInstalledOnce: a plugin under test named like the core sloprail plugin
// (a git worktree's own copy, while sr-test was built from another checkout) is the one plugin
// installed: its case sees exactly one plugin folder, its own, and `sr-test agent` does not fail with
// "symlink ...: file exists" for the doubled name.
func TestSrTestPluginCopyOfCoreIsInstalledOnce(t *testing.T) {
	e := New(t)
	root := t.TempDir()
	write(t, root, "marketplace/plugins/sloprail/.claude-plugin/plugin.json", `{"name":"sloprail"}`)
	write(t, root, "marketplace/plugins/sloprail/.sloprail/gate/g/tests/once/test.sh",
		"#!/bin/sh\ncase \"$SR_TEST_PLUGIN_DIR\" in \"\"|*:*) echo \"plugins: $SR_TEST_PLUGIN_DIR\"; exit 1;; esac\n"+
			"case \"$SR_TEST_PLUGIN_DIR\" in *marketplace/plugins/sloprail) exit 0;; esac\nexit 1")

	res := e.CLIDirect(root, "sr-test", "run")
	sc := bufio.NewScanner(strings.NewReader(res.Output))
	found := false
	for sc.Scan() {
		var r struct{ Subject, Status, Output string }
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.Subject == "" {
			continue
		}
		found = true
		if r.Status != "pass" {
			t.Errorf("%s: %s %s", r.Subject, r.Status, r.Output)
		}
	}
	if !found {
		t.Fatalf("no case ran:\n%s", res.Output)
	}
}
