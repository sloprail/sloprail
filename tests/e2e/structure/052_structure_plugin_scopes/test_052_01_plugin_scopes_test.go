package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// mdmapStructure is what a plugin like mdmap would ship: it owns `.mdmap/`, lets
// only mind-map files be written there, never a .tmp, and a key file under keys/
// (so a project deny can be shown vetoing something the plugin allows).
const mdmapStructure = `scope:
  - glob: ".mdmap/"
allow:
  - glob: ".mdmap/mindmap/*/mindmap.yaml"
  - glob: ".mdmap/**/*.tmp"
  - glob: ".mdmap/keys/*.secret"
deny:
  - glob: ".mdmap/**/*.tmp"
`

// projectStructure is the project's own, tree-wide: docs/ and a notes folder
// that happens to sit INSIDE mdmap's scope (which its allow must not widen), and
// a veto on *.secret anywhere.
const projectStructure = `allow:
  - glob: "docs/**"
  - glob: ".mdmap/notes/*.md"
deny:
  - glob: "**/*.secret"
`

// unescaped strips the JSON escaping the mock's stream wraps a refusal in (a
// quote inside a tool result arrives as \\\"), so an assertion can name
// `plugin "mdmap"` the way a person reads it.
func unescaped(output string) string { return strings.ReplaceAll(output, `\`, "") }

// assertRefused checks a write was refused, did not land, and the refusal
// carries every given substring (the deciding source).
func assertRefused(t *testing.T, e *harness.Env, proj, path string, refused bool, output string, want ...string) {
	t.Helper()
	if !refused {
		t.Fatalf("the write to %s was not refused:\n%s", path, output)
	}
	if e.Exists(proj, path) {
		t.Errorf("the refused write to %s LANDED", path)
	}
	for _, w := range want {
		if !strings.Contains(unescaped(output), w) {
			t.Errorf("the refusal does not name %q:\n%s", w, output)
		}
	}
}

// T052_01: a plugin's structure is enforced inside its scope with NO project
// structure — a write the plugin does not allow is refused, naming the plugin
// and the scope; the allowed shape passes; a write outside the scope passes.
//
// Fails on the old engine: there the plugin's structure (a singleton with no
// notion of scope) became the tree-wide allowlist, so the write outside the
// scope (src/main.go) was refused, and its refusal named no scope.
func TestT052_01_PluginStructureGovernsOnlyItsScope(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.EnablePluginShippingStructure(proj, "mdmap", mdmapStructure)

	stray := e.Run(proj, "s-052-01a", "write a stray file into .mdmap", Turns("done",
		Write("w1", ".mdmap/stray.md", "# stray"),
	))
	assertRefused(t, e, proj, ".mdmap/stray.md", stray.Refused(), stray.Output,
		`plugin "mdmap"`, `scope ".mdmap/"`)

	allowed := e.Run(proj, "s-052-01b", "write a mind map", Turns("done",
		Write("w1", ".mdmap/mindmap/a/mindmap.yaml", "root: a\n"),
	))
	if allowed.Refused() || !e.Exists(proj, ".mdmap/mindmap/a/mindmap.yaml") {
		t.Errorf("the shape the plugin allows was refused or did not land:\n%s", allowed.Output)
	}

	denied := e.Run(proj, "s-052-01c", "write a temp file into .mdmap", Turns("done",
		Write("w1", ".mdmap/mindmap/a/cache.tmp", "x"),
	))
	assertRefused(t, e, proj, ".mdmap/mindmap/a/cache.tmp", denied.Refused(), denied.Output,
		"`deny` exception", `plugin "mdmap"`)

	outside := e.Run(proj, "s-052-01d", "write outside the plugin's scope", Turns("done",
		Write("w1", "src/main.go", "package main\n"),
	))
	if outside.Refused() || !e.Exists(proj, "src/main.go") {
		t.Errorf("a write OUTSIDE the plugin's scope was refused — a plugin may lock down only its own scope:\n%s", outside.Output)
	}
}

// T052_02: the project's and a plugin's structures combine.
//
//   - an owned path allowed only by the plugin passes (the project's allow does
//     not have to cover it);
//   - a path inside the scope allowed only by the PROJECT is refused (no
//     widening);
//   - a project deny vetoes inside the scope;
//   - an unowned path the project does not allow is refused, and one it does
//     allow passes.
//
// Fails on the old engine: the project's structure shadowed the plugin's, so the
// owned mind-map path (not in the project's allow) was refused, and the notes
// path (in the project's allow) was permitted.
func TestT052_02_ProjectAndPluginCombine(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.StructureGate(proj, projectStructure)
	e.EnablePluginShippingStructure(proj, "mdmap", mdmapStructure)

	owned := e.Run(proj, "s-052-02a", "write a mind map", Turns("done",
		Write("w1", ".mdmap/mindmap/a/mindmap.yaml", "root: a\n"),
	))
	if owned.Refused() || !e.Exists(proj, ".mdmap/mindmap/a/mindmap.yaml") {
		t.Errorf("an owned path the plugin allows (and the project does not) was refused:\n%s", owned.Output)
	}

	widened := e.Run(proj, "s-052-02b", "write a note under .mdmap", Turns("done",
		Write("w1", ".mdmap/notes/n.md", "# note"),
	))
	assertRefused(t, e, proj, ".mdmap/notes/n.md", widened.Refused(), widened.Output,
		`plugin "mdmap"`, "does not widen")

	vetoed := e.Run(proj, "s-052-02c", "write a key under .mdmap", Turns("done",
		Write("w1", ".mdmap/keys/k.secret", "shh"),
	))
	assertRefused(t, e, proj, ".mdmap/keys/k.secret", vetoed.Refused(), vetoed.Output,
		"this project's structure gate", "last word")

	unowned := e.Run(proj, "s-052-02d", "write source", Turns("done",
		Write("w1", "src/main.go", "package main\n"),
	))
	assertRefused(t, e, proj, "src/main.go", unowned.Refused(), unowned.Output,
		"this project's structure gate", "deny-by-default")

	docs := e.Run(proj, "s-052-02e", "write docs", Turns("done",
		Write("w1", "docs/guide.md", "# guide"),
	))
	if docs.Refused() || !e.Exists(proj, "docs/guide.md") {
		t.Errorf("an unowned path the project allows was refused:\n%s", docs.Output)
	}
}

// T052_03: two plugins whose scopes overlap. A write in the overlap is refused as
// an ownership conflict naming BOTH plugins; with literal scopes the overlap is
// also reported at session start, naming both.
//
// Fails on the old engine: the earlier plugin's structure won and the later was
// shadowed, so the overlap write was decided by one plugin alone (and permitted,
// since it allows it) — nothing named both.
func TestT052_03_OverlappingScopesRefuseNamingBoth(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	// A literal scope and a wildcard one that both cover .mdmap/ at the root.
	e.EnablePluginShippingStructure(proj, "mdmap", "scope:\n  - glob: \".mdmap/\"\nallow:\n  - glob: \".mdmap/**\"\n")
	e.EnablePluginShippingStructure(proj, "mapper", "scope:\n  - glob: \"**/.mdmap/\"\nallow:\n  - glob: \"**/.mdmap/**\"\n")

	res := e.Run(proj, "s-052-03a", "write into .mdmap", Turns("done",
		Write("w1", ".mdmap/x.md", "# x"),
	))
	assertRefused(t, e, proj, ".mdmap/x.md", res.Refused(), res.Output,
		"ownership conflict", `plugin "mdmap"`, `plugin "mapper"`)

	// Only the wildcard plugin owns a NESTED .mdmap — decided by it alone.
	nested := e.Run(proj, "s-052-03b", "write a nested .mdmap file", Turns("done",
		Write("w1", "pkg/.mdmap/y.md", "# y"),
	))
	if nested.Refused() || !e.Exists(proj, "pkg/.mdmap/y.md") {
		t.Errorf("a write only in one plugin's scope was not decided by that plugin alone:\n%s", nested.Output)
	}
}

// T052_04: two plugins with overlapping LITERAL scopes are reported when the
// session starts, naming both — before any agent meets the conflict.
func TestT052_04_LiteralOverlapReportedAtStart(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.EnablePluginShippingStructure(proj, "mdmap", "scope:\n  - glob: \".mdmap/\"\nallow:\n  - glob: \".mdmap/*.md\"\n")
	e.EnablePluginShippingStructure(proj, "mapper", "scope:\n  - glob: \".mdmap/mindmap/\"\nallow:\n  - glob: \".mdmap/mindmap/*.yaml\"\n")

	start := e.CLI(proj, "session", "start")
	for _, want := range []string{`plugin "mdmap"`, `plugin "mapper"`, "ownership conflict", "mdmap/structure", "mapper/structure"} {
		if !strings.Contains(start.Output, want) {
			t.Errorf("session start did not report the scope overlap (%q missing):\n%s", want, start.Output)
		}
	}
	// And session start lists each plugin's owned scope.
	for _, want := range []string{`plugin "mdmap"'s structure gate owns .mdmap/`, `plugin "mapper"'s structure gate owns .mdmap/mindmap/`} {
		if !strings.Contains(start.Output, want) {
			t.Errorf("session start did not list the owned scope %q:\n%s", want, start.Output)
		}
	}
}

// T052_05: `disabled: [<plugin>/structure]` in the project's config lifts the
// plugin's structure — its former scope is unowned, and with no project
// structure a write there is permitted.
func TestT052_05_DisabledPluginStructureIsLifted(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.EnablePluginShippingStructure(proj, "mdmap", mdmapStructure)
	e.DisablePluginGuardrail(proj, "mdmap/structure")

	res := e.Run(proj, "s-052-05", "write a stray file into .mdmap", Turns("done",
		Write("w1", ".mdmap/stray.md", "# stray"),
	))
	if res.Refused() || !e.Exists(proj, ".mdmap/stray.md") {
		t.Errorf("the disabled plugin structure still refused a write in its former scope:\n%s", res.Output)
	}
}

// T052_06: a plugin structure with no `scope` is invalid — reported at session
// start (naming the plugin and the missing scope) and NOT enforced.
//
// Fails on the old engine: a scope-less plugin structure loaded as the tree-wide
// allowlist, so the write outside .mdmap/ was refused and nothing was reported.
func TestT052_06_PluginStructureWithoutScopeIsReportedNotEnforced(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.EnablePluginShippingStructure(proj, "mdmap", "allow:\n  - glob: \".mdmap/mindmap/*/mindmap.yaml\"\n")

	start := e.CLI(proj, "session", "start")
	for _, want := range []string{"not loaded", `plugin "mdmap"`, "must declare a `scope`", "disabled: [mdmap/structure]"} {
		if !strings.Contains(start.Output, want) {
			t.Errorf("session start did not report the scope-less plugin structure (%q missing):\n%s", want, start.Output)
		}
	}

	res := e.Run(proj, "s-052-06", "write anywhere", Turns("done",
		Write("w1", "src/main.go", "package main\n"),
		Write("w2", ".mdmap/stray.md", "# stray"),
	))
	if res.Refused() {
		t.Errorf("an invalid plugin structure was enforced:\n%s", res.Output)
	}
	if !e.Exists(proj, "src/main.go") || !e.Exists(proj, ".mdmap/stray.md") {
		t.Errorf("writes did not land under an invalid (unenforced) plugin structure")
	}
}

// T052_07: a Bash-derived write is gated exactly like a Write — a redirect into
// the plugin's scope that it does not allow is refused before it runs, while
// one producing the allowed shape runs.
func TestT052_07_BashWriteIsGatedLikeAWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.EnablePluginShippingStructure(proj, "mdmap", mdmapStructure)
	e.WriteFile(proj, ".mdmap/README", "the mdmap folder\n")

	res := e.Run(proj, "s-052-07a", "echo into .mdmap", Turns("done",
		Bash("b1", "echo x > .mdmap/stray.md"),
	))
	assertRefused(t, e, proj, ".mdmap/stray.md", res.Refused(), res.Output, `plugin "mdmap"`)

	ok := e.Run(proj, "s-052-07b", "echo a mind map", Turns("done",
		Bash("b1", "mkdir -p .mdmap/mindmap/a && echo 'root: a' > .mdmap/mindmap/a/mindmap.yaml"),
	))
	if ok.Refused() || !e.Exists(proj, ".mdmap/mindmap/a/mindmap.yaml") {
		t.Errorf("a Bash write producing the allowed shape was refused or did not land:\n%s", ok.Output)
	}
}

// T052_08: a DELETE inside a plugin's scope is not gated by the structure gate —
// the structure gate governs writes, as it always has.
func TestT052_08_DeleteInsideScopeIsNotGated(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.EnablePluginShippingStructure(proj, "mdmap", mdmapStructure)
	e.WriteFile(proj, ".mdmap/old.md", "# old\n")

	res := e.Run(proj, "s-052-08", "remove an old file", Turns("done",
		Bash("b1", "rm .mdmap/old.md"),
	))
	if res.Refused() {
		t.Errorf("a delete inside the plugin's scope was refused by the structure gate:\n%s", res.Output)
	}
	if e.Exists(proj, ".mdmap/old.md") {
		t.Errorf("the delete did not happen")
	}
}

// T052_09: `sr-file declarations --plugin` validates what a plugin ships by the
// plugin rules — a scoped structure loads and lists its scope; a scope-less one
// is refused (exit 1) naming the missing scope.
func TestT052_09_DeclarationsPluginFlag(t *testing.T) {
	e := New(t)
	proj := e.Project()
	good := e.EnablePluginShippingStructure(proj, "mdmap", mdmapStructure)
	bad := e.EnablePluginShippingStructure(proj, "broken", "allow:\n  - glob: \"x/*.md\"\n")

	ok := e.CLIDirect(proj, "sr-file", "declarations", "--plugin", "mdmap", good)
	if ok.Code != 0 || !strings.Contains(ok.Output, `structure gate from plugin "mdmap": owns .mdmap/`) {
		t.Errorf("declarations --plugin did not load and list the plugin's scoped structure (exit %d):\n%s", ok.Code, ok.Output)
	}

	ko := e.CLIDirect(proj, "sr-file", "declarations", "--plugin", "broken", bad)
	if ko.Code != 1 || !strings.Contains(ko.Output, "broken/structure") || !strings.Contains(ko.Output, "must declare a `scope`") {
		t.Errorf("declarations --plugin did not refuse a scope-less plugin structure (exit %d):\n%s", ko.Code, ko.Output)
	}
}
