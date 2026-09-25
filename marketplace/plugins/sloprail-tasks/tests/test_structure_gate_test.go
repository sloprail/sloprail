package e2e

import (
	"strings"
	"testing"
)

// TestPluginStructureGateParses proves the plugin's OWN structure gate file,
// .sloprail/file-guard/structure.yaml, is valid and loads as a PLUGIN's
// structure -- via `sr-file declarations --plugin sloprail-tasks`, the same
// inspection surface a consumer checks before installing (see
// authoring-guardrails' structure-gate.md). `--plugin` matters: without it the
// loader applies the PROJECT rules instead (no `scope` allowed at all), and
// this file's `scope: [{glob: "memories/tasks/"}]` would then be a load
// error -- the two rule sets are genuinely different, not a formality.
//
// Uses e.CLIDirect ("sr-file", ...), NOT a bare exec.Command -- CLIDirect runs
// the binary THIS TEST RUN BUILT (harness.build), under e.binDir, the same
// resolution every other CLI-driving test in this suite uses. A bare
// exec.Command("sr-file", …) would instead resolve whatever sr-file happens to
// sit on the ambient $PATH, which may be a different build lacking a flag or
// subcommand this one has (measured: it is, in this repo's dev environment).
func TestPluginStructureGateParses(t *testing.T) {
	e := New(t)
	root := pluginRoot(t)

	res := e.CLIDirect(root, "sr-file", "declarations", "--plugin", pluginName, root)
	if res.Code != 0 {
		t.Fatalf("sr-file declarations --plugin %s %s failed (exit %d):\n%s", pluginName, root, res.Code, res.Output)
	}
	got := res.Output
	if !strings.Contains(got, "owns memories/tasks/") {
		t.Fatalf("sr-file declarations did not report the structure gate as owning memories/tasks/:\n%s", got)
	}
	if strings.Contains(got, "0 allow") {
		t.Errorf("the structure gate loaded with no allow entries -- structure.yaml is present but empty:\n%s", got)
	}
}

// TestStructureGate_AllowedWriteInsideScopePermits: a TASK.md write, the shape
// the plugin's own allow list names first, lands inside memories/tasks/ -- the
// plugin's structure gate is the ONLY thing that could refuse a bare Write at
// a fresh path (no citation is required by the structure gate itself; the
// task's OTHER guards run too, so the body is grounded and the judge stubbed
// PASS to isolate the structure decision).
func TestStructureGate_AllowedWriteInsideScopePermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-structure-allowed"
	tp := e.TranscriptPath(proj, sess)
	body := "The user asked to " + cite("migrate the auth module", tp, 1) + "."
	doc := task("backlog", "P1", body)

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", taskPath, doc),
	))
	if res.Refused() {
		t.Fatalf("a TASK.md write matching the plugin's own allow list was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, taskPath) {
		t.Errorf("an admitted write did not land on disk")
	}
}

// structureDisallowedPath sits inside the plugin's memories/tasks/ scope but
// matches NONE of its allow entries: nested more than one level below the
// task folder (gates/*.{sh,md} and the "anything directly under the task
// folder" catch-all both require exactly one more segment, not several).
const structureDisallowedPath = "memories/tasks/auth/migrate-tokens/random/deep/file.bin"

// TestStructureGate_DisallowedWriteInsideScopeRefused: a write inside the
// plugin's OWNED folder that matches none of its allow entries is refused BY
// THE PLUGIN -- proving the scope decides here, not the (absent) project
// structure, which per the composition rule never gets a say inside a single
// owning plugin's scope.
func TestStructureGate_DisallowedWriteInsideScopeRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)

	res := e.Run(proj, "s-structure-disallowed", authPrompt, Turns("done",
		Write("w1", structureDisallowedPath, "whatever\n"),
	))
	if !res.Refused() {
		t.Fatalf("a write inside memories/tasks/ matching no allow entry was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, structureDisallowedPath) {
		t.Errorf("the structure gate let a disallowed write land")
	}
	if !res.Saw("structure gate") {
		t.Errorf("the refusal was not the structure gate's:\n%s", res.Output)
	}
	if !res.Saw(pluginName) {
		t.Errorf("the refusal did not attribute the decision to the owning plugin:\n%s", res.Output)
	}
}

// TestStructureGate_WriteOutsideScopeUnaffected: a write OUTSIDE
// memories/tasks/ entirely is untouched by this plugin's structure gate --
// its scope has no say there at all (see structure-gate.md, "Outside the
// scope the plugin has no say"). The harness project has no structure gate of
// its own, so with no owner deciding, the write is permitted -- proving the
// plugin's scope is not silently acting as a project-wide deny-by-default,
// the exact regression the earlier (pre-composition) format risked.
func TestStructureGate_WriteOutsideScopeUnaffected(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, e, proj)

	const outsidePath = "src/auth.go"
	res := e.Run(proj, "s-structure-outside", authPrompt, Turns("done",
		Write("w1", outsidePath, "package auth\n"),
	))
	if res.Refused() {
		t.Fatalf("a write outside memories/tasks/ was refused, although the plugin's structure gate should have no say there:\n%s", res.Output)
	}
	if !e.Exists(proj, outsidePath) {
		t.Errorf("an admitted write outside the plugin's scope did not land on disk")
	}
}
