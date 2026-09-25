package e2e

import (
	"strings"
	"testing"
)

// projectStructureYAML is the project's OWN structure gate: it only knows about
// src/, and never mentions memories/ at all — the point of composition is that
// it does not have to.
const projectStructureYAML = `allow:
  - glob: "src/**"
`

// pluginStructureYAML is a plugin's scoped structure gate — the shape the task
// says other plugins (sloprail-tasks, sloprail-content) are shipping for real:
// a `scope` naming the folder the plugin owns, and an `allow` inside it.
const pluginStructureYAML = `scope:
  - glob: "memories/tasks/**"
allow:
  - regex: '^memories/tasks/[a-z0-9-]+/[a-z0-9-]+/[^/].*$'
`

// T052_01: a plugin's own scoped allow permits a path inside its scope that the
// PROJECT's allowlist never lists.
//
// This is the headline composition property: the project does not need to
// enumerate a plugin-owned shape in its own structure.yaml, because the
// plugin's own allow covers it. Before this change, the plugin's structure gate
// would have been Shadowed the moment the project shipped one of its own — this
// proves the plugin's piece is actually consulted.
func TestT052_01_PluginScopeAllowsWhatProjectDoesNotList(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.StructureGate(proj, projectStructureYAML)
	e.EnablePluginShippingStructureGate(proj, "sloprail-tasks", pluginStructureYAML)

	res := e.Run(proj, "s-052-01", "write a task note", Turns("done",
		Write("w1", "memories/tasks/eng/fix-bug/notes.md", "# notes"),
	))

	if res.Refused() {
		t.Errorf("a write inside the plugin's own scope and allow was refused, even though "+
			"the project's allowlist never mentions memories/ — the plugin's piece was not consulted:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/tasks/eng/fix-bug/notes.md") {
		t.Errorf("the plugin-allowed write did not land")
	}
}

// T052_02: the project's own allow still works, unaffected by the plugin being
// installed — composition adds permissions the plugin owns, it does not take
// any away from the project's own scope.
func TestT052_02_ProjectsOwnAllowStillWorksWithPluginInstalled(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.StructureGate(proj, projectStructureYAML)
	e.EnablePluginShippingStructureGate(proj, "sloprail-tasks", pluginStructureYAML)

	res := e.Run(proj, "s-052-02", "write source", Turns("done",
		Write("w1", "src/main.go", "package main"),
	))

	if res.Refused() {
		t.Errorf("a write inside the project's own allow was refused with the plugin installed:\n%s", res.Output)
	}
	if !e.Exists(proj, "src/main.go") {
		t.Errorf("the project-allowed write did not land")
	}
}

// T052_03: a path outside EVERY covering gate's scope is unaffected by the
// plugin — no structure gate has an opinion, so the write is permitted (there is
// nothing here for either the project's unscoped-but-narrow allow or the
// plugin's narrow scope to admit, and NEITHER refuses it because the project's
// own gate in this test is scoped too).
//
// To prove "no covering gate => no opinion" rather than "the project's own
// unscoped gate happens to deny by default", this test gives the PROJECT a
// SCOPED structure gate as well (scoped to src/), so docs/ is outside every
// covering file's scope and the claim under test is unambiguous.
func TestT052_03_PathOutsideEveryScopeIsUnaffected(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.StructureGate(proj, `scope:
  - glob: "src/**"
allow:
  - glob: "src/**"
`)
	e.EnablePluginShippingStructureGate(proj, "sloprail-tasks", pluginStructureYAML)

	res := e.Run(proj, "s-052-03", "write outside every scope", Turns("done",
		Write("w1", "docs/readme.md", "# readme"),
	))

	if res.Refused() {
		t.Errorf("a path outside every covering gate's scope was refused — no gate should have "+
			"an opinion about it:\n%s", res.Output)
	}
	if !e.Exists(proj, "docs/readme.md") {
		t.Errorf("the write with no covering structure gate did not land")
	}
}

// T052_04: a plugin structure gate WITHOUT `scope` does not enforce — a plugin
// structure gate must declare the paths it owns, or installing it would lock
// the whole consuming project's tree, so it is refused at load and blocks
// nothing (the same "an invalid declaration blocks nothing" stance
// 013_broken_declaration_is_not_silent pins for the other natures).
//
// The REPORT half ("reported invalid, naming the plugin and the missing scope")
// is not observable through this mock: a permitted action carries neither
// stdout nor stderr back to the agent (see that package's doc comment), and an
// invalid declaration is exactly the permitted case. That half is pinned where
// it can be read: internal/declaration's loader tests
// (TestNewWithPlugins_UnscopedPluginStructureIsInvalid) assert the Invalid
// record itself, naming the plugin and the missing scope.
func TestT052_04_UnscopedPluginStructureIsInvalid(t *testing.T) {
	e := New(t)
	proj := e.Project()
	// No project structure gate at all — so if the plugin's unscoped gate DID
	// load, deny-by-default would refuse everything not under memories/updates/.
	e.EnablePluginShippingStructureGate(proj, "sloprail-tasks", `allow:
  - glob: "memories/updates/*.md"
`)

	res := e.Run(proj, "s-052-04", "write anywhere", Turns("done",
		Write("w1", "src/main.go", "package main"),
	))

	if res.Refused() {
		t.Errorf("an unscoped plugin structure gate enforced anyway — it should have been "+
			"reported invalid and blocked nothing:\n%s", res.Output)
	}
	if !e.Exists(proj, "src/main.go") {
		t.Errorf("the write did not land even though no valid structure gate covers it")
	}
}

// T052_05: a `deny` in a covering plugin refuses, even for a path the plugin's
// own allow would otherwise permit.
func TestT052_05_DenyInCoveringPluginRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.EnablePluginShippingStructureGate(proj, "sloprail-tasks", `scope:
  - glob: "memories/tasks/**"
allow:
  - glob: "memories/tasks/**"
deny:
  - glob: "memories/tasks/secret/**"
`)

	blocked := e.Run(proj, "s-052-05a", "write a secret task file", Turns("done",
		Write("w1", "memories/tasks/secret/x.md", "shh"),
	))
	if !blocked.Refused() {
		t.Errorf("a write under the plugin's own deny exception was not refused:\n%s", blocked.Output)
	}
	if e.Exists(proj, "memories/tasks/secret/x.md") {
		t.Errorf("the plugin-denied write LANDED")
	}

	allowed := e.Run(proj, "s-052-05b", "write an ordinary task file", Turns("done",
		Write("w2", "memories/tasks/eng/fix-bug/notes.md", "# notes"),
	))
	if allowed.Refused() {
		t.Errorf("a write under the plugin's allow but outside its deny was refused:\n%s", allowed.Output)
	}
	if !e.Exists(proj, "memories/tasks/eng/fix-bug/notes.md") {
		t.Errorf("the plugin-allowed write did not land")
	}
}

// T052_06: disabling `<plugin>/structure` removes JUST that plugin's piece —
// the write it used to allow is now refused (nothing else covers it), while a
// path the PROJECT's own structure gate covers is unaffected.
func TestT052_06_DisablingPluginStructureRemovesItsAllows(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.StructureGate(proj, projectStructureYAML)
	e.EnablePluginShippingStructureGate(proj, "sloprail-tasks", pluginStructureYAML)
	e.DisablePluginGuardrail(proj, "sloprail-tasks/structure")

	// The path the plugin used to allow is now refused — nothing else covers it.
	blocked := e.Run(proj, "s-052-06a", "write a task note", Turns("done",
		Write("w1", "memories/tasks/eng/fix-bug/notes.md", "# notes"),
	))
	if !blocked.Refused() {
		t.Errorf("a write the DISABLED plugin's structure gate used to allow was not refused:\n%s", blocked.Output)
	}
	if e.Exists(proj, "memories/tasks/eng/fix-bug/notes.md") {
		t.Errorf("the write landed despite the plugin's structure gate being disabled")
	}

	// The project's own structure gate is unaffected by disabling the plugin's.
	allowed := e.Run(proj, "s-052-06b", "write source", Turns("done",
		Write("w2", "src/main.go", "package main"),
	))
	if allowed.Refused() {
		t.Errorf("disabling the plugin's structure gate also disabled the project's own:\n%s", allowed.Output)
	}
	if !e.Exists(proj, "src/main.go") {
		t.Errorf("the project-allowed write did not land")
	}
}

// T052_07: the refusal for a path with no matching allow among its covering
// files names the covering file(s) — an author reading it knows where to add an
// allow, not just that something somewhere refused it.
func TestT052_07_RefusalNamesCoveringFile(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.EnablePluginShippingStructureGate(proj, "sloprail-tasks", `scope:
  - glob: "memories/tasks/**"
allow:
  - glob: "memories/tasks/only-this-shape/**"
`)

	res := e.Run(proj, "s-052-07", "write a wrongly shaped task file", Turns("done",
		Write("w1", "memories/tasks/elsewhere/x.md", "x"),
	))

	if !res.Refused() {
		t.Fatalf("a path inside the plugin's scope but matching no allow was not refused:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "sloprail-tasks/structure") {
		t.Errorf("the refusal does not name the covering plugin's qualified key:\n%s", res.Output)
	}
}
