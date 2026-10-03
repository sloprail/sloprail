package harness

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// project builds a project directory with the given settings files, each written
// verbatim so a test can exercise malformed JSON as easily as well-formed.
func project(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, ".claude", name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// installPlugin creates a plugin installation in a fake cache, returning the
// home directory the cache lives under.
func installPlugin(t *testing.T, home, marketplace, name, version string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "plugins", "cache", marketplace, name, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	return dir
}

// enabled renders a settings file enabling the named plugins.
func enabled(pairs ...string) string {
	var b strings.Builder
	b.WriteString(`{"enabledPlugins":{`)
	for i := 0; i < len(pairs); i += 2 {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"` + pairs[i] + `":` + pairs[i+1])
	}
	b.WriteString(`}}`)
	return b.String()
}

// TestResolve_ReadsTheProjectsOwnEnabledPlugins is the headline: discovery is a
// property of what the REPO installed, read from the repo's settings, and not of
// what happened to run.
func TestResolve_ReadsTheProjectsOwnEnabledPlugins(t *testing.T) {
	home := t.TempDir()
	want := installPlugin(t, home, "acme-marketplace", "acme", "1.0.0")
	proj := project(t, map[string]string{
		"settings.json": enabled("acme@acme-marketplace", "true"),
	})

	res, err := Resolve(proj, home)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Unresolved) != 0 {
		t.Fatalf("an installed plugin was reported unresolved: %v", res.Unresolved)
	}
	if len(res.Roots) != 1 {
		t.Fatalf("want 1 root, got %d: %+v", len(res.Roots), res.Roots)
	}
	if res.Roots[0].Dir != want {
		t.Errorf("resolved to %q, want %q", res.Roots[0].Dir, want)
	}
	// The NAME must come off the settings key, because that is what the user
	// wrote and what a refusal and a disable-list entry will quote back.
	if res.Roots[0].Plugin.Name != "acme" {
		t.Errorf("plugin name %q, want %q", res.Roots[0].Plugin.Name, "acme")
	}
	if res.Roots[0].Plugin.Marketplace != "acme-marketplace" {
		t.Errorf("marketplace %q, want %q", res.Roots[0].Plugin.Marketplace, "acme-marketplace")
	}
}

// TestResolve_APluginNotEnabledIsNotDiscovered is the control for the test
// above. Without it, "we found the plugin" would also pass against an engine
// that simply globbed the cache and ignored settings entirely — which is the
// mechanism being replaced, and would defeat the entire point.
func TestResolve_APluginNotEnabledIsNotDiscovered(t *testing.T) {
	home := t.TempDir()
	installPlugin(t, home, "acme-marketplace", "acme", "1.0.0")

	// Installed in the cache, but the project's settings say nothing about it.
	proj := project(t, map[string]string{"settings.json": `{}`})

	res, err := Resolve(proj, home)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Roots) != 0 {
		t.Fatalf("a plugin the project never enabled was discovered — discovery is reading "+
			"the cache rather than the project's settings: %+v", res.Roots)
	}
}

// TestResolve_LocalSettingsOverrideProjectSettings holds the MEASURED precedence.
//
// Measured against Claude Code 2.1.218 with a probe plugin whose SessionStart
// hook touches a file — the table is reproduced in resolveEnabled's comment.
// settings.local.json wins in BOTH directions, which is the part an assumption
// would have got wrong: it is easy to guess that the personal layer can only ADD
// plugins, and under that guess row 1 below would resolve the plugin and a
// developer could never switch one off for themselves.
func TestResolve_LocalSettingsOverrideProjectSettings(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		wantOn  bool
		because string
	}{
		{
			name: "local false beats project true",
			files: map[string]string{
				"settings.json":       enabled("acme@acme-marketplace", "true"),
				"settings.local.json": enabled("acme@acme-marketplace", "false"),
			},
			wantOn:  false,
			because: "the personal layer must be able to switch off a plugin the project enabled",
		},
		{
			name: "local true beats project false",
			files: map[string]string{
				"settings.json":       enabled("acme@acme-marketplace", "false"),
				"settings.local.json": enabled("acme@acme-marketplace", "true"),
			},
			wantOn:  true,
			because: "the personal layer must be able to switch on a plugin the project disabled",
		},
		{
			name:    "local alone",
			files:   map[string]string{"settings.local.json": enabled("acme@acme-marketplace", "true")},
			wantOn:  true,
			because: "a plugin enabled only in the gitignored personal layer is still enabled",
		},
		{
			name:    "project alone",
			files:   map[string]string{"settings.json": enabled("acme@acme-marketplace", "true")},
			wantOn:  true,
			because: "the committed layer enables plugins for everyone",
		},
		{
			name:    "project false alone",
			files:   map[string]string{"settings.json": enabled("acme@acme-marketplace", "false")},
			wantOn:  false,
			because: "an explicit false is not an enablement",
		},
		{
			name:    "local false alone",
			files:   map[string]string{"settings.local.json": enabled("acme@acme-marketplace", "false")},
			wantOn:  false,
			because: "an explicit false is not an enablement",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			installPlugin(t, home, "acme-marketplace", "acme", "1.0.0")
			proj := project(t, tc.files)

			res, err := Resolve(proj, home)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			gotOn := len(res.Roots) == 1
			if gotOn != tc.wantOn {
				t.Errorf("enabled=%v, want %v — %s", gotOn, tc.wantOn, tc.because)
			}
		})
	}
}

// TestResolve_AnEnabledPluginThatCannotBeFoundIsReported is the safety property
// the whole design turns on.
//
// If a cache layout moves or a manifest schema is bumped, this must produce a
// NAMED report rather than an empty result. An empty result is indistinguishable
// from a project with no plugins, and the guardrails would silently stop firing
// — the exact failure this product exists to prevent.
func TestResolve_AnEnabledPluginThatCannotBeFoundIsReported(t *testing.T) {
	home := t.TempDir() // nothing installed
	proj := project(t, map[string]string{
		"settings.json": enabled("ghost@ghost-marketplace", "true"),
	})

	res, err := Resolve(proj, home)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Roots) != 0 {
		t.Fatalf("resolved a plugin that is not installed: %+v", res.Roots)
	}
	if len(res.Unresolved) != 1 {
		t.Fatalf("an enabled plugin that could not be located was SKIPPED rather than reported. "+
			"That is the silent no-op this design exists to prevent: got %d reports", len(res.Unresolved))
	}
	u := res.Unresolved[0]
	if u.Key != "ghost@ghost-marketplace" {
		t.Errorf("the report does not name the plugin as the settings file did: %q", u.Key)
	}
	msg := u.Message()
	for _, want := range []string{"ghost@ghost-marketplace", "could not be located", "NOT enforcing"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message does not contain %q — a user cannot act on it:\n%s", want, msg)
		}
	}
	// The paths tried are what turn the report into a diagnosis.
	if len(u.Tried) == 0 || !strings.Contains(msg, "Looked in:") {
		t.Errorf("the report does not say where it looked, so a maintainer cannot tell a "+
			"missing install from a moved layout:\n%s", msg)
	}
}

// TestResolve_AMalformedSettingsFileIsAnError: a settings file that will not
// parse must not read as "this project enabled nothing".
//
// Returning an empty set here would disable every shipped guardrail on account
// of a stray comma, silently. The callers turn this error into a refusal.
func TestResolve_AMalformedSettingsFileIsAnError(t *testing.T) {
	home := t.TempDir()
	proj := project(t, map[string]string{"settings.json": `{"enabledPlugins": {`})

	if _, err := Resolve(proj, home); err == nil {
		t.Fatal("a settings file that could not be parsed was treated as a project with no " +
			"plugins — a syntax error must not silently disable every installed guardrail")
	}
}

// TestResolve_NoSettingsIsNotAnError. Most repositories have no .claude
// settings at all, and sloprail must be silent in them rather than refusing on
// its own behalf.
func TestResolve_NoSettingsIsNotAnError(t *testing.T) {
	res, err := Resolve(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("a project with no settings files errored: %v", err)
	}
	if len(res.Roots) != 0 || len(res.Unresolved) != 0 {
		t.Errorf("a project with no settings produced findings: %+v", res)
	}
}

// TestLiveVersion_PicksTheRecordedVersionNotJustAnyDirectory is the measured
// case that rules out the simpler design.
//
// The tempting simplification was to skip installed_plugins.json and take the
// only directory under <marketplace>/<plugin>/. Measured on the author's real
// machine, that is unsound: a10n-spec-capability has 0.0.1 and 0.0.8 in the
// cache and figma has 2.2.50 and 2.2.81, because Claude Code leaves superseded
// versions behind on upgrade. Listing alone picks a stale copy, whose guardrails
// then load and fire from an old version with nothing looking wrong.
// The recorded version is deliberately the LOWER of the two installed. A
// fixture where the live version is also the highest cannot tell "read the
// manifest" apart from "take the newest directory" — both answer correctly, and
// the manifest read could be deleted without the test noticing. Pinning the
// live version below a stale higher one is what makes this test about the
// manifest at all.
//
// This is not a contrived ordering: a downgrade, or a pinned older install
// alongside a newer one left behind by an upgrade, produces exactly this shape.
func TestLiveVersion_PicksTheRecordedVersionNotJustAnyDirectory(t *testing.T) {
	home := t.TempDir()
	live := installPlugin(t, home, "acme-marketplace", "acme", "0.0.1")
	installPlugin(t, home, "acme-marketplace", "acme", "0.0.8")

	manifest := `{"version":2,"plugins":{"acme@acme-marketplace":[` +
		`{"scope":"project","version":"0.0.1","installPath":"live"}]}}`
	writeManifest(t, home, manifest)

	proj := project(t, map[string]string{"settings.json": enabled("acme@acme-marketplace", "true")})
	res, err := Resolve(proj, home)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Roots) != 1 {
		t.Fatalf("want 1 root, got %+v (unresolved %+v)", res.Roots, res.Unresolved)
	}
	if res.Roots[0].Dir != live {
		t.Errorf("resolved to %q, want the recorded live version %q — a stale cached version "+
			"loads and fires with nothing looking wrong", res.Roots[0].Dir, live)
	}
}

// TestLiveVersion_AnUnknownManifestSchemaFallsBackRatherThanFailing is the
// other half of the schema-move defence.
//
// When installed_plugins.json goes to version 3, this package must not read it
// with a version 2 parser (confidently wrong) and must not give up (silently
// blind). It falls back to the directory listing, which is exact whenever there
// is one version — the overwhelmingly common case — so a schema bump degrades to
// a wrong-version risk for multi-version plugins instead of to total blindness.
// The version 3 fixture still carries a `version` field that a version 2 parser
// would happily read — and it names the STALE directory. That is what makes this
// a real test of the schema check rather than of the fallback alone: a resolver
// that ignored `"version": 3` and decoded the file anyway would resolve to
// 1.0.0, and only a resolver that refuses to interpret an unknown schema reaches
// the listing and answers 2.0.0.
//
// The shape is the plausible one. A schema bump that REUSED the `version` key
// for something else — a per-scope pin, a range, a manifest revision — is
// exactly how a silent misread happens, because every field still decodes and
// nothing errors.
func TestLiveVersion_AnUnknownManifestSchemaFallsBackRatherThanFailing(t *testing.T) {
	home := t.TempDir()
	installPlugin(t, home, "acme-marketplace", "acme", "1.0.0")
	want := installPlugin(t, home, "acme-marketplace", "acme", "2.0.0")
	writeManifest(t, home,
		`{"version":3,"plugins":{"acme@acme-marketplace":[{"version":"1.0.0","installPath":"stale"}]}}`)

	proj := project(t, map[string]string{"settings.json": enabled("acme@acme-marketplace", "true")})
	res, err := Resolve(proj, home)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Roots) != 1 {
		t.Fatalf("a manifest schema bump made an installed plugin unresolvable — the fallback "+
			"to the directory listing is what keeps a schema move from disabling every "+
			"guardrail: roots=%+v unresolved=%+v", res.Roots, res.Unresolved)
	}
	if res.Roots[0].Dir != want {
		t.Errorf("an unrecognised manifest schema was decoded with the version-2 parser and "+
			"believed: resolved %q, want the listing's answer %q", res.Roots[0].Dir, want)
	}
}

// TestLiveVersion_OrdersVersionsNumerically. With no usable manifest record and
// several versions installed, the newest is the best guess — and "newest" must
// be a numeric comparison, or 0.0.10 sorts below 0.0.9 and the guess silently
// becomes wrong after ten releases.
func TestLiveVersion_OrdersVersionsNumerically(t *testing.T) {
	home := t.TempDir()
	installPlugin(t, home, "acme-marketplace", "acme", "0.0.9")
	want := installPlugin(t, home, "acme-marketplace", "acme", "0.0.10")
	// No manifest at all, so the listing is the only source.

	proj := project(t, map[string]string{"settings.json": enabled("acme@acme-marketplace", "true")})
	res, err := Resolve(proj, home)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Roots) != 1 || res.Roots[0].Dir != want {
		t.Errorf("versions were ordered as strings, so 0.0.10 lost to 0.0.9: got %+v", res.Roots)
	}
}

// TestResolve_DirectoryMarketplaceResolvesFromItsSource.
//
// Measured on Claude Code 2.1.218: a marketplace whose source is a local
// directory is loaded FROM that directory and never enters the cache — the
// plugin reports a CLAUDE_PLUGIN_ROOT inside the source tree. The e2e mock
// behaves the same way, leaving its cache empty. This is also the case every
// plugin AUTHOR is in, so a cache-only resolver would report the people most
// likely to notice as having no plugins at all.
func TestResolve_DirectoryMarketplaceResolvesFromItsSource(t *testing.T) {
	home := t.TempDir()
	src := t.TempDir()
	want := filepath.Join(src, "marketplace", "plugins", "acme")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatalf("mkdir marketplace plugin: %v", err)
	}

	settings := `{"enabledPlugins":{"acme@acme-marketplace":true},` +
		`"extraKnownMarketplaces":{"acme-marketplace":{"source":{"source":"directory","path":"` + src + `"}}}}`
	proj := project(t, map[string]string{"settings.json": settings})

	res, err := Resolve(proj, home)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Roots) != 1 {
		t.Fatalf("a plugin from a directory-sourced marketplace was not resolved — this is the "+
			"case every plugin author is in: roots=%+v unresolved=%+v", res.Roots, res.Unresolved)
	}
	if res.Roots[0].Dir != want {
		t.Errorf("resolved to %q, want %q", res.Roots[0].Dir, want)
	}
}

// TestResolve_ARelativeDirectoryMarketplacePathResolvesAgainstTheProjectRoot.
// A relative `path` (the shape this repo's own settings.json uses: `"./"`) must
// not be at the mercy of the resolving process's own OS working directory — a
// hook subprocess whose cwd differs from the project root (measured to happen
// for a nested/child session) would otherwise resolve it to the wrong place, or
// nowhere, and every guardrail the plugin ships goes quietly unresolved.
func TestResolve_ARelativeDirectoryMarketplacePathResolvesAgainstTheProjectRoot(t *testing.T) {
	home := t.TempDir()
	proj := project(t, map[string]string{
		"settings.json": `{"enabledPlugins":{"acme@acme-marketplace":true},` +
			`"extraKnownMarketplaces":{"acme-marketplace":{"source":{"source":"directory","path":"./"}}}}`,
	})
	want := filepath.Join(proj, "marketplace", "plugins", "acme")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatalf("mkdir marketplace plugin: %v", err)
	}

	res, err := Resolve(proj, home)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Roots) != 1 {
		t.Fatalf("a plugin from a relative directory-sourced marketplace was not resolved: roots=%+v unresolved=%+v",
			res.Roots, res.Unresolved)
	}
	if res.Roots[0].Dir != want {
		t.Errorf("resolved to %q, want %q (the project root, not the test process's own cwd)", res.Roots[0].Dir, want)
	}
}

// TestResolve_AKeyThatIsNotPluginAtMarketplaceIsReported. A malformed key is
// still the project saying it enabled something, so it is named rather than
// dropped.
func TestResolve_AKeyThatIsNotPluginAtMarketplaceIsReported(t *testing.T) {
	proj := project(t, map[string]string{"settings.json": enabled("nonsense", "true")})

	res, err := Resolve(proj, t.TempDir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Unresolved) != 1 {
		t.Fatalf("a malformed enabledPlugins key was dropped silently: %+v", res)
	}
	if !strings.Contains(res.Unresolved[0].Message(), "nonsense") {
		t.Errorf("the report does not quote the key the user wrote: %s", res.Unresolved[0].Message())
	}
}

// TestResolve_OrderIsDeterministic. Order decides which plugin's rule shadows
// another's when two ship the same name, so it must not depend on map iteration.
func TestResolve_OrderIsDeterministic(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"zulu", "alpha", "mike"} {
		installPlugin(t, home, "m", name, "1.0.0")
	}
	proj := project(t, map[string]string{
		"settings.json": enabled("zulu@m", "true", "alpha@m", "true", "mike@m", "true"),
	})

	var first []string
	for i := 0; i < 10; i++ {
		res, err := Resolve(proj, home)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		var got []string
		for _, r := range res.Roots {
			got = append(got, r.Plugin.Key())
		}
		if i == 0 {
			first = got
			continue
		}
		if strings.Join(got, ",") != strings.Join(first, ",") {
			t.Fatalf("resolution order varies between runs (%v then %v) — which plugin's rule "+
				"shadows another's would depend on map iteration", first, got)
		}
	}
	if strings.Join(first, ",") != "alpha@m,mike@m,zulu@m" {
		t.Errorf("order is not the settings key order a user can predict: %v", first)
	}
}

// writeManifest writes installed_plugins.json under a fake home.
func writeManifest(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plugins: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "installed_plugins.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// TestResolve_AKeyWhoseHalvesAreNotDirectoryNamesCannotEscapeItsRoot is the
// traversal case, on BOTH branches that join a key half into a path.
//
// Reproduced before the isPathSafe check existed: `filepath.Join` CLEANS its
// result, so a `..` in either half does not produce a path that fails to exist —
// it produces a different, EXISTING path outside the root meant to bound it.
// Root.Dir is read for `<root>/guardrails/<name>/GUARDRAIL.md`, so either branch
// pointed rule loading at an arbitrary directory whose rules would then enforce
// looking exactly like the plugin's own.
//
// Each case computes its traversal RELATIVE to the path actually being joined,
// and creates the target, so a passing test proves the name was refused rather
// than that the escape happened to miss.
func TestResolve_AKeyWhoseHalvesAreNotDirectoryNamesCannotEscapeItsRoot(t *testing.T) {
	// The cache branch: <cache>/<marketplace>/<plugin>/<version>. A plugin name
	// that climbs out of <cache>/<marketplace> reaches a tree of its own.
	t.Run("plugin name escapes the cache", func(t *testing.T) {
		home := t.TempDir()
		target := filepath.Join(home, "evil")
		if err := os.MkdirAll(filepath.Join(target, "0.0.1"), 0o755); err != nil {
			t.Fatal(err)
		}
		cacheMarket := filepath.Join(CacheRoot(home), "m")
		rel, err := filepath.Rel(cacheMarket, target)
		if err != nil {
			t.Fatal(err)
		}
		assertRefused(t, project(t, map[string]string{"settings.json": enabled(rel+"@m", "true")}),
			home, rel+"@m")
	})

	// The same branch through the OTHER half of the key.
	t.Run("marketplace name escapes the cache", func(t *testing.T) {
		home := t.TempDir()
		target := filepath.Join(home, "evil")
		if err := os.MkdirAll(filepath.Join(target, "p", "0.0.1"), 0o755); err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(CacheRoot(home), target)
		if err != nil {
			t.Fatal(err)
		}
		assertRefused(t, project(t, map[string]string{"settings.json": enabled("p@"+rel, "true")}),
			home, "p@"+rel)
	})

	// The directory-marketplace branch: <mktRoot>/plugins/<plugin> and the
	// three siblings beside it, any of which a `..` climbs out of.
	t.Run("plugin name escapes a directory marketplace", func(t *testing.T) {
		home := t.TempDir()
		proj := t.TempDir()
		mkt := filepath.Join(proj, "mkt")
		if err := os.MkdirAll(filepath.Join(mkt, "plugins"), 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(proj, "evil")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(filepath.Join(mkt, "plugins"), target)
		if err != nil {
			t.Fatal(err)
		}
		key := rel + "@local"
		if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"enabledPlugins":{"` + key + `":true},` +
			`"extraKnownMarketplaces":{"local":{"source":{"source":"directory","path":"` + mkt + `"}}}}`
		if err := os.WriteFile(filepath.Join(proj, ".claude", "settings.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		assertRefused(t, proj, home, key)
	})

	// A separator alone is enough to reject, without any `..` to clean.
	for _, key := range []string{"a/b@m", "p@m/n"} {
		t.Run(key, func(t *testing.T) {
			home := t.TempDir()
			assertRefused(t, project(t, map[string]string{"settings.json": enabled(key, "true")}), home, key)
		})
	}

	// The bare dot names traverse WITHOUT containing a separator, so they are a
	// distinct branch and not a special case of the one above. Each target is
	// created, so these fail if the name is honoured rather than passing because
	// the escape happened to land somewhere empty.
	t.Run("..@m climbs to the cache root", func(t *testing.T) {
		// <cache>/m/.. is <cache> itself, whose children are marketplace
		// directories — so a version directory placed there resolves.
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(CacheRoot(home), "0.0.1"), 0o755); err != nil {
			t.Fatal(err)
		}
		assertRefused(t, project(t, map[string]string{"settings.json": enabled("..@m", "true")}),
			home, "..@m")
	})

	t.Run("p@.. climbs out of the cache root", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(filepath.Dir(CacheRoot(home)), "p", "0.0.1"), 0o755); err != nil {
			t.Fatal(err)
		}
		assertRefused(t, project(t, map[string]string{"settings.json": enabled("p@..", "true")}),
			home, "p@..")
	})

	t.Run(".@local is the marketplace's own plugins dir", func(t *testing.T) {
		home := t.TempDir()
		proj := t.TempDir()
		mkt := filepath.Join(proj, "mkt")
		if err := os.MkdirAll(filepath.Join(mkt, "plugins"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"enabledPlugins":{".@local":true},` +
			`"extraKnownMarketplaces":{"local":{"source":{"source":"directory","path":"` + mkt + `"}}}}`
		if err := os.WriteFile(filepath.Join(proj, ".claude", "settings.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		assertRefused(t, proj, home, ".@local")
	})
}

// assertRefused holds a settings key to the invariant: it resolves to no root,
// and it is REPORTED rather than dropped, quoting the key the user wrote.
func assertRefused(t *testing.T, proj, home, key string) {
	t.Helper()
	res, err := Resolve(proj, home)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Roots) != 0 {
		t.Fatalf("key %q resolved to a root and must not have: %+v", key, res.Roots)
	}
	// Refused, not dropped: the project enabled something and sloprail is not
	// loading it, which is the invariant of this whole package.
	if len(res.Unresolved) != 1 {
		t.Fatalf("key %q was dropped silently: %+v", key, res)
	}
	if !strings.Contains(res.Unresolved[0].Message(), key) {
		t.Errorf("the report does not quote the key the user wrote: %s", res.Unresolved[0].Message())
	}
}

// TestResolve_ADirectoryMarketplaceSourceIsHonouredAsWritten is the boundary of
// the check above: the marketplace's own `path` is a PATH by declaration, and a
// project that points one outside its tree meant to. Only the KEY halves are
// constrained, because only they are names being used as path components.
func TestResolve_ADirectoryMarketplaceSourceIsHonouredAsWritten(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	proj := project(t, map[string]string{"settings.json": `{"enabledPlugins":{"p@local":true},
		"extraKnownMarketplaces":{"local":{"source":{"source":"directory","path":"` + outside + `"}}}}`})

	res, err := Resolve(proj, home)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Roots) != 1 || res.Roots[0].Dir != filepath.Join(outside, "p") {
		t.Fatalf("a declared marketplace path must be used as written: %+v %+v", res.Roots, res.Unresolved)
	}
}

// TestResolve_TheManifestCannotNameAVersionOutsideTheCache. The version half of
// the path comes from the manifest, which is a third attacker-reachable string.
// It cannot traverse, because a recorded version is only used when it matches an
// entry of the real directory listing — asserted here so that property is not
// refactored away.
func TestResolve_TheManifestCannotNameAVersionOutsideTheCache(t *testing.T) {
	home := t.TempDir()
	installPlugin(t, home, "m", "p", "0.0.1")
	installPlugin(t, home, "m", "p", "0.0.2")
	if err := os.MkdirAll(filepath.Join(home, "evil"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, home, `{"version":2,"plugins":{"p@m":[{"version":"../../../evil"}]}}`)
	proj := project(t, map[string]string{"settings.json": enabled("p@m", "true")})

	res, err := Resolve(proj, home)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Roots) != 1 {
		t.Fatalf("want one root, got %+v", res.Roots)
	}
	want := filepath.Join(home, ".claude", "plugins", "cache", "m", "p", "0.0.2")
	if res.Roots[0].Dir != want {
		t.Errorf("a traversing manifest version was honoured: got %s want %s", res.Roots[0].Dir, want)
	}
}

func writeSession(t *testing.T, home, name, body string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProcessOfSession_FindsTheLiveProcessOfASessionId(t *testing.T) {
	home := t.TempDir()
	pid := os.Getpid()
	writeSession(t, home, "1", `{"pid":1,"sessionId":"other","procStart":"x"}`)
	writeSession(t, home, "me", `{"pid":`+itoa(pid)+`,"sessionId":"sess-1","procStart":"Mon Oct  3 10:00:00 2026"}`)
	proc, ok := ProcessOfSession(home, "sess-1")
	if !ok || proc.PID != pid || proc.ProcStart != "Mon Oct  3 10:00:00 2026" {
		t.Fatalf("got %+v %v", proc, ok)
	}
	if _, ok := ProcessOfSession(home, "nope"); ok {
		t.Fatal("an unknown session has no process")
	}
	if _, ok := ProcessOfSession(t.TempDir(), "sess-1"); ok {
		t.Fatal("no sessions directory, no process")
	}
	if _, ok := ProcessOfSession(home, ""); ok {
		t.Fatal("an empty id names nothing")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestProcessGone(t *testing.T) {
	home := t.TempDir()
	pid := os.Getpid()
	proc := Process{PID: pid, ProcStart: "s1"}

	if gone, known := ProcessGone(home, proc); gone || known {
		t.Fatalf("no sessions directory is unknown, not gone: %v %v", gone, known)
	}
	writeSession(t, home, itoa(pid), `{"pid":`+itoa(pid)+`,"sessionId":"a","procStart":"s1"}`)
	if gone, known := ProcessGone(home, proc); gone || !known {
		t.Fatalf("a live process with its file is not gone: %v %v", gone, known)
	}
	if gone, known := ProcessGone(home, Process{PID: pid, ProcStart: "other"}); !gone || !known {
		t.Fatalf("a pid reused by another start is gone: %v %v", gone, known)
	}
	if gone, known := ProcessGone(home, Process{PID: 999999, ProcStart: "s1"}); !gone || !known {
		t.Fatalf("a process with no file is gone: %v %v", gone, known)
	}
	writeSession(t, home, "999998", `{"pid":999998,"sessionId":"a","procStart":"s1"}`)
	if gone, known := ProcessGone(home, Process{PID: 999998, ProcStart: "s1"}); !gone || !known {
		t.Fatalf("a file whose pid is dead is gone: %v %v", gone, known)
	}
	writeSession(t, home, itoa(pid), `not json`)
	if gone, known := ProcessGone(home, proc); !gone || !known {
		t.Fatalf("a file that cannot be read fails toward judging: %v %v", gone, known)
	}
	if gone, known := ProcessGone(home, Process{}); gone || known {
		t.Fatalf("no process recorded is unknown: %v %v", gone, known)
	}
}
