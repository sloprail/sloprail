package guardrail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Attribution is the whole of question 3: a refusal has to say which plugin a
// rule came from, or the reader looks in .sloprail/guardrails/ and finds nothing.
func TestOrigin_AttributionAndQualifiedName(t *testing.T) {
	project := Declaration{Name: "no-secrets"}
	if got := project.Qualified(); got != "no-secrets" {
		t.Errorf("a project's own rule qualified to %q, want the bare name", got)
	}
	if got := project.Attribution(); got != `"no-secrets"` {
		t.Errorf("a project's rule attributed as %q — naming this project in every refusal is noise", got)
	}

	shipped := Declaration{Name: "no-secrets", Origin: Origin{Plugin: "sloprail", Root: "/cache/sloprail/0.0.1"}}
	if got := shipped.Qualified(); got != "sloprail/no-secrets" {
		t.Errorf("a plugin's rule qualified to %q, want sloprail/no-secrets", got)
	}
	if got := shipped.Attribution(); !strings.Contains(got, "sloprail") || !strings.Contains(got, "no-secrets") {
		t.Errorf("a plugin's rule attributed as %q — a refusal that does not name the plugin "+
			"sends the reader to a file that is not there", got)
	}
}


// at builds the plugin origins a store is constructed from, taking each
// plugin's NAME from its installation path the way the resolver takes it from
// the settings key that enabled it.
//
// The store no longer derives names from paths — that was moved to
// internal/harness, which reads the name a user actually wrote. These tests keep
// naming plugins by path because it reads well, so this helper does the same
// mapping the resolver would: `<...>/<plugin>/<version>` means the plugin is the
// second-to-last component.
func installedAt(roots ...string) []Origin {
	origins := make([]Origin, 0, len(roots))
	for _, root := range roots {
		origins = append(origins, Origin{Plugin: filepath.Base(filepath.Dir(root)), Root: root})
	}
	return origins
}

// writeDeclIn puts a minimal valid declaration directly under a guardrails
// directory. Distinct from store_test.go's writeGuardrail, which takes a store
// ROOT and appends `guardrails/` itself: these tests write into a plugin's
// guardrails directory as well as a project's, so the directory is the argument.
func writeDeclIn(t *testing.T, guardrailsDir, name, body string) {
	t.Helper()
	dir := filepath.Join(guardrailsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "GUARDRAIL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write declaration: %v", err)
	}
}

// minimalDecl is a declaration that parses and binds nothing, which is all these
// resolution tests need: they are about WHICH rules load, not what they enforce.
const minimalDecl = `---
hooks: {}
---

# A rule
`

// The headline capability: a guardrail inside a plugin is in force without the
// project copying it.
func TestResolve_LoadsAGuardrailShippedInAPlugin(t *testing.T) {
	root := t.TempDir()
	dot := filepath.Join(root, ".sloprail")
	plugin := filepath.Join(root, "plug", "sloprail", "0.0.1")
	writeDeclIn(t, pluginGuardrailsDir(plugin), "authoring-slop", minimalDecl)

	res, err := NewWithPlugins(dot, installedAt(plugin)).Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(res.Declarations) != 1 {
		t.Fatalf("loaded %d guardrails, want 1 — the plugin's rule is not in force", len(res.Declarations))
	}
	d := res.Declarations[0]
	if d.Name != "authoring-slop" {
		t.Errorf("loaded %q, want authoring-slop", d.Name)
	}
	if !d.Origin.FromPlugin() || d.Origin.Plugin != "sloprail" {
		t.Errorf("origin = %+v, want it attributed to plugin sloprail", d.Origin)
	}
	// The hook's working directory has to be INSIDE the installation, or a
	// shipped `./check-rules.sh` resolves to nothing.
	if !strings.HasPrefix(d.Dir, plugin) {
		t.Errorf("Dir = %q, want it under the plugin installation %q — a shipped hook's "+
			"command resolves relative to this", d.Dir, plugin)
	}
}

// Question 1: precedence, and that the shadowing is REPORTED rather than silent.
func TestResolve_ProjectRuleWinsAndTheShadowingIsReported(t *testing.T) {
	root := t.TempDir()
	dot := filepath.Join(root, ".sloprail")
	plugin := filepath.Join(root, "plug", "sloprail", "0.0.1")

	writeDeclIn(t, filepath.Join(dot, "guardrails"), "authoring-slop", minimalDecl)
	writeDeclIn(t, pluginGuardrailsDir(plugin), "authoring-slop", minimalDecl)

	res, err := NewWithPlugins(dot, installedAt(plugin)).Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(res.Declarations) != 1 {
		t.Fatalf("loaded %d guardrails, want 1 — the same name must not be in force twice", len(res.Declarations))
	}
	if res.Declarations[0].Origin.FromPlugin() {
		t.Fatalf("the PLUGIN's rule won; a project must be able to override a rule it did not write")
	}

	if len(res.Shadowed) != 1 {
		t.Fatalf("shadowed = %v, want exactly one report — a project that displaced a rule "+
			"and was never told believes it has two protections and has one", res.Shadowed)
	}
	sh := res.Shadowed[0]
	if sh.Name != "authoring-slop" || sh.Plugin != "sloprail" {
		t.Errorf("shadow = %+v, want it to name both the rule and the plugin", sh)
	}
	if !strings.Contains(sh.Message(), "sloprail") || !strings.Contains(sh.Message(), "authoring-slop") {
		t.Errorf("shadow message %q names neither side", sh.Message())
	}
}

// Two plugins shipping one name: the earlier-listed wins, and the loser is still
// reported. Precedence between plugins has to be decidable and stated, or which
// rule enforces depends on directory-read order.
func TestResolve_EarlierPluginWinsAndTheLaterIsReported(t *testing.T) {
	root := t.TempDir()
	dot := filepath.Join(root, ".sloprail")
	first := filepath.Join(root, "p1", "alpha", "1.0")
	second := filepath.Join(root, "p2", "beta", "1.0")
	writeDeclIn(t, pluginGuardrailsDir(first), "shared", minimalDecl)
	writeDeclIn(t, pluginGuardrailsDir(second), "shared", minimalDecl)

	res, err := NewWithPlugins(dot, installedAt(first, second)).Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Declarations) != 1 || res.Declarations[0].Origin.Plugin != "alpha" {
		t.Fatalf("declarations = %+v, want only alpha's — the earlier entry wins", res.Declarations)
	}
	if len(res.Shadowed) != 1 || res.Shadowed[0].Plugin != "beta" {
		t.Fatalf("shadowed = %+v, want beta's rule reported as displaced", res.Shadowed)
	}

	// The message has to name BOTH plugins and say which one is live. This is
	// the case a consumer is least able to diagnose — they chose neither the
	// collision nor the ordering — so "one of your rules is not running" without
	// saying which is running is not a usable report.
	msg := res.Shadowed[0].Message()
	for _, want := range []string{"alpha", "beta", "shared"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the plugin-over-plugin report %q does not name %q", msg, want)
		}
	}
	if strings.Contains(msg, "in this project takes precedence") {
		t.Errorf("a plugin-over-plugin collision was reported as a project override: %q", msg)
	}
}

// Question 2: a consumer switches off a plugin's rule from their OWN side.
func TestResolve_ProjectDisablesAPluginsRuleWithoutEditingIt(t *testing.T) {
	root := t.TempDir()
	dot := filepath.Join(root, ".sloprail")
	plugin := filepath.Join(root, "plug", "sloprail", "0.0.1")
	writeDeclIn(t, pluginGuardrailsDir(plugin), "authoring-slop", minimalDecl)
	writeDeclIn(t, pluginGuardrailsDir(plugin), "other-rule", minimalDecl)

	if err := os.MkdirAll(dot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dot, ConfigFile),
		[]byte("disabled:\n  - sloprail/authoring-slop\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	res, err := NewWithPlugins(dot, installedAt(plugin)).Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// The disabled one is gone entirely, not merely marked: its kinds must not
	// enter the bound set and its hooks must never be dispatched.
	for _, d := range res.Declarations {
		if d.Name == "authoring-slop" {
			t.Fatalf("a disabled plugin rule is still in force: %+v", d)
		}
	}
	// And the plugin's OTHER rule is untouched. Disabling is per rule, not per
	// plugin — otherwise the only way to drop one rule is to lose them all.
	var kept []string
	for _, d := range res.Declarations {
		kept = append(kept, d.Qualified())
	}
	if len(kept) != 1 || kept[0] != "sloprail/other-rule" {
		t.Fatalf("in force = %v, want only sloprail/other-rule", kept)
	}
}

// The qualified name is what makes disabling precise. A project disabling a
// plugin's rule must not also switch off its own rule of the same name.
func TestResolve_DisablingAPluginsRuleDoesNotDisableTheProjectsOwn(t *testing.T) {
	root := t.TempDir()
	dot := filepath.Join(root, ".sloprail")
	plugin := filepath.Join(root, "plug", "other", "1.0")
	writeDeclIn(t, filepath.Join(dot, "guardrails"), "shared-name", minimalDecl)
	writeDeclIn(t, pluginGuardrailsDir(plugin), "unrelated", minimalDecl)

	if err := os.WriteFile(filepath.Join(dot, ConfigFile),
		[]byte("disabled:\n  - other/shared-name\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	res, err := NewWithPlugins(dot, installedAt(plugin)).Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var found bool
	for _, d := range res.Declarations {
		if d.Name == "shared-name" && !d.Origin.FromPlugin() {
			found = true
		}
	}
	if !found {
		t.Fatalf("disabling %q switched off the PROJECT's own rule of that name; "+
			"the disable list must be keyed on the qualified name", "other/shared-name")
	}
}

// A config that exists and cannot be parsed must not be read as "no overrides".
// Defaulting there would silently re-enable every rule the project had switched
// off, and would look exactly like a project that never wrote one.
func TestResolve_AnUnparseableConfigIsAnErrorNotAnEmptyOne(t *testing.T) {
	root := t.TempDir()
	dot := filepath.Join(root, ".sloprail")
	writeDeclIn(t, filepath.Join(dot, "guardrails"), "a-rule", minimalDecl)
	if err := os.WriteFile(filepath.Join(dot, ConfigFile), []byte("disabled: [unclosed\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := NewWithPlugins(dot, nil).Resolve(nil); err == nil {
		t.Fatal("an unparseable config resolved cleanly — a project stating overrides " +
			"nobody can read must not be treated as a project stating none")
	}
}

// A project with no config has made no overrides, which is the ordinary state.
func TestLoadConfig_AbsentIsNotAnError(t *testing.T) {
	cfg, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadConfig on a project with no config: %v", err)
	}
	if len(cfg.Disabled) != 0 {
		t.Fatalf("disabled = %v, want none", cfg.Disabled)
	}
}

// A plugin that ships no guardrails at all is the overwhelmingly common case —
// most plugins are skills and hooks. It must not be an error.
func TestResolve_APluginWithNoGuardrailsIsNotAnError(t *testing.T) {
	root := t.TempDir()
	dot := filepath.Join(root, ".sloprail")
	writeDeclIn(t, filepath.Join(dot, "guardrails"), "mine", minimalDecl)

	res, err := NewWithPlugins(dot, installedAt(filepath.Join(root, "plug-with-nothing"))).Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Declarations) != 1 || res.Declarations[0].Name != "mine" {
		t.Fatalf("declarations = %+v, want the project's own rule only", res.Declarations)
	}
}

// Uninstalling a plugin removes its rules: with the directory no longer named,
// nothing of it is in force. The other half of "installing puts them in force".
func TestResolve_APluginNoLongerNamedContributesNothing(t *testing.T) {
	root := t.TempDir()
	dot := filepath.Join(root, ".sloprail")
	plugin := filepath.Join(root, "plug", "sloprail", "0.0.1")
	writeDeclIn(t, pluginGuardrailsDir(plugin), "authoring-slop", minimalDecl)

	installed, err := NewWithPlugins(dot, installedAt(plugin)).Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(installed.Declarations) != 1 {
		t.Fatalf("the control failed: the plugin's rule was not in force to begin with")
	}

	uninstalled, err := NewWithPlugins(dot, nil).Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(uninstalled.Declarations) != 0 {
		t.Fatalf("declarations = %+v, want none once the plugin is no longer installed",
			uninstalled.Declarations)
	}
}

// A broken declaration inside a plugin must be attributable and disableable.
// Without the second half, one broken shipped rule wedges every consuming
// project with no remedy but uninstalling the plugin — the consumer cannot fix a
// file they do not own.
func TestResolve_ABrokenPluginRuleCanBeSwitchedOffByTheConsumer(t *testing.T) {
	root := t.TempDir()
	dot := filepath.Join(root, ".sloprail")
	plugin := filepath.Join(root, "plug", "sloprail", "0.0.1")
	writeDeclIn(t, pluginGuardrailsDir(plugin), "broken", "no frontmatter at all\n")

	res, err := NewWithPlugins(dot, installedAt(plugin)).Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Invalid) != 1 {
		t.Fatalf("invalid = %+v, want the broken shipped rule reported", res.Invalid)
	}
	if res.Invalid[0].Qualified() != "sloprail/broken" {
		t.Fatalf("qualified = %q, want sloprail/broken — a consumer cannot switch off "+
			"a rule they cannot name", res.Invalid[0].Qualified())
	}
	if !strings.Contains(res.Invalid[0].Attribution(), "sloprail") {
		t.Fatalf("attribution = %q does not name the plugin", res.Invalid[0].Attribution())
	}

	if err := os.MkdirAll(dot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dot, ConfigFile),
		[]byte("disabled:\n  - sloprail/broken\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	off, err := NewWithPlugins(dot, installedAt(plugin)).Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(off.Invalid) != 0 {
		t.Fatalf("invalid = %+v, want none once disabled — otherwise a plugin's broken rule "+
			"wedges the project with no remedy it owns", off.Invalid)
	}
}

// A project rule shadows a plugin rule of the same name even when the plugin's
// is BROKEN — and this is the severe half of precedence.
//
// A declaration that cannot be parsed refuses every action outright, since
// nothing can be said about what it guarded. So if shadowing skipped the invalid
// ones, a project that had already overridden `dup` with a sound rule of its own
// would still be blocked at every tool call by the plugin's broken `dup`, and
// the override it correctly made would buy it nothing. Measured before the fix:
// `sr-session pre-tool` denied a plain write with exactly that reason.
func TestResolve_AProjectRuleShadowsEvenABrokenPluginRuleOfThatName(t *testing.T) {
	root := t.TempDir()
	dot := filepath.Join(root, ".sloprail")
	plugin := filepath.Join(root, "plug", "sloprail", "0.0.1")

	writeDeclIn(t, filepath.Join(dot, "guardrails"), "dup", minimalDecl)
	writeDeclIn(t, pluginGuardrailsDir(plugin), "dup", "no frontmatter at all\n")

	res, err := NewWithPlugins(dot, installedAt(plugin)).Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(res.Invalid) != 0 {
		t.Fatalf("invalid = %+v, want none — the project overrode this name, so the plugin's "+
			"unreadable declaration must not go on refusing every action", res.Invalid)
	}
	if len(res.Declarations) != 1 || res.Declarations[0].Origin.FromPlugin() {
		t.Fatalf("declarations = %+v, want the project's own rule in force", res.Declarations)
	}
	// Still reported, because every shadow is: a project that displaced a rule
	// should hear about it whatever state that rule was in.
	if len(res.Shadowed) != 1 || res.Shadowed[0].Name != "dup" {
		t.Fatalf("shadowed = %+v, want the displaced broken rule reported", res.Shadowed)
	}
}

// New (no plugins) must behave exactly as it did: callers like `sr-file validate`
// read one project's declarations and have no business seeing plugin rules.
func TestNew_ReadsOnlyTheProjectsOwnGuardrails(t *testing.T) {
	root := t.TempDir()
	dot := filepath.Join(root, ".sloprail")
	writeDeclIn(t, filepath.Join(dot, "guardrails"), "mine", minimalDecl)

	decls, invalid, err := New(dot).LoadWith(nil)
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if len(invalid) != 0 {
		t.Fatalf("invalid = %+v, want none", invalid)
	}
	if len(decls) != 1 || decls[0].Origin.FromPlugin() {
		t.Fatalf("declarations = %+v, want one project-owned rule", decls)
	}
}
