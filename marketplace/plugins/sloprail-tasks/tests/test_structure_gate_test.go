package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPluginStructureGateParses proves the plugin's OWN structure gate file,
// .sloprail/file-guard/structure.yaml, is valid and loads -- via the product's
// own `sr-file declarations` over the plugin root, the same inspection surface
// the skill's own load check uses (see authoring-guardrails). This is
// deliberately NOT an end-to-end test of the gate's ENFORCEMENT: composition
// of a plugin's `scope`d structure gate with a consumer project's own gate is
// not yet implemented on this engine (see installPluginTree's comment on why
// this plugin's own e2e fixtures exclude the file from what they install), so
// there is nothing this suite can drive through the mock to observe it acting
// scoped. Once composition lands, an enforcement-level e2e belongs here or
// alongside the generic structure-gate suite (tests/e2e/fileguard/…) -- this
// test is the floor: the file the plugin ships is not malformed.
//
// Uses e.CLIDirect ("sr-file", ...), NOT a bare exec.Command -- CLIDirect runs
// the binary THIS TEST RUN BUILT (harness.build), under e.binDir, the same
// resolution every other CLI-driving test in this suite uses. A bare
// exec.Command("sr-file", …) would instead resolve whatever sr-file happens to
// sit on the ambient $PATH, which may be a different build lacking a
// subcommand this one has (measured: it is, in this repo's dev environment).
func TestPluginStructureGateParses(t *testing.T) {
	e := New(t)
	root := pluginRoot(t)
	gatePath := filepath.Join(root, ".sloprail", "file-guard", "structure.yaml")

	res := e.CLIDirect(root, "sr-file", "declarations", root)
	if res.Code != 0 {
		t.Fatalf("sr-file declarations %s failed (exit %d):\n%s", root, res.Code, res.Output)
	}
	got := res.Output
	if !strings.Contains(got, "structure gate: present") {
		t.Fatalf("sr-file declarations did not report the structure gate as loaded (looked for %s):\n%s", gatePath, got)
	}
	if strings.Contains(got, "0 allow") {
		t.Errorf("the structure gate loaded with no allow entries -- structure.yaml is present but empty:\n%s", got)
	}
}
