// Package harness resolves which plugins a PROJECT has installed, by reading
// the harness's own configuration.
//
// # Why this package exists, and why it is allowed to be harness-specific
//
// A guardrail that ships inside a plugin has to be FOUND, and the only thing
// that knows which plugins a project installed is the project's own harness
// configuration. That is not an accident of implementation — it is the fact
// itself. The repo is what made the decision to install a plugin, so the repo's
// settings are the truth about it, and any discovery mechanism that infers the
// set from something else is inferring rather than reading.
//
// The rejected alternative was to have the plugin tell the engine where it
// lives, through an environment variable set in the plugin's own hooks.json.
// It kept this package from existing, and it was wrong for a reason no amount
// of isolation fixes: it only ever discovers a plugin that FIRED A HOOK. A
// plugin that ships guardrails and registers no hooks is invisible to it, and
// discovery becomes a property of what happened to run rather than of what the
// repo installed. Those are different sets, and only one of them is the answer
// to the question being asked.
//
// So the knowledge has to live somewhere, and this file is where it is kept.
// The precedent is internal/transcript/claudecode.go, which is the only place
// that names Claude Code's own JSONL field spellings and does not pretend
// otherwise. A second harness gets a second file beside this one rather than a
// widening of these types.
//
// # What is assumed, and what happens when the assumption breaks
//
// Every Claude Code path, filename and schema assumption in sloprail is in this
// file. There are exactly four, and each is named at its use:
//
//  1. `.claude/settings.json` and `.claude/settings.local.json` hold an
//     `enabledPlugins` object, keyed `<plugin>@<marketplace>`.
//  2. Plugin installations are cached under `<cache>/<marketplace>/<plugin>/<version>/`.
//  3. `installed_plugins.json` records which version is live, under `"version": 2`.
//  4. A marketplace whose source is a local directory is loaded FROM that
//     directory rather than from the cache.
//
// The danger this design is built against is a schema move: `installed_plugins.json`
// going to version 3, sloprail reading zero plugins, and every shipped guardrail
// silently ceasing to fire. That is the exact silent no-op this product exists
// to prevent, so it must not be possible here.
//
// The defence is that a settings file naming an enabled plugin whose directory
// cannot be found is REPORTED, never skipped. Resolve returns those as
// Unresolved alongside the roots it did find, and every caller surfaces them.
// A schema move therefore turns into "enabled plugin X could not be located",
// which is a sentence a user can act on, rather than into silence, which is not.
// See Unresolved for why this is a report rather than a refusal.
package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SettingsFiles are the project-level settings files that can enable a plugin,
// in ASCENDING order of precedence: a later file's verdict on a plugin wins
// over an earlier one's.
//
// Both files matter and neither is optional. settings.json is the committed
// layer the team shares; settings.local.json is the gitignored personal one. A
// discovery that read only the first would miss a plugin a developer enabled
// for themselves, and one that read only the second would miss every plugin the
// project ships to everyone.
//
// The ORDER is measured, not assumed — see resolveEnabled for the table. It is
// the same order Claude Code itself applies, which is what makes sloprail's set
// of guardrails the set the user's harness is actually running.
var SettingsFiles = []string{"settings.json", "settings.local.json"}

// settings is the fragment of a Claude Code settings file this package reads.
//
// Deliberately a narrow struct rather than a map: everything else in these
// files — permissions, hooks, env, MCP servers — is none of sloprail's business,
// and a decoder that ignores it cannot be broken by it changing. A settings file
// growing a new top-level key must not disturb plugin discovery.
type settings struct {
	// EnabledPlugins maps `<plugin>@<marketplace>` to whether it is on.
	//
	// A pointer-free bool map is right here because ABSENT and FALSE mean the
	// same thing at this layer — not enabled — while the DISTINCTION between
	// them matters across files, and that is handled by iterating each file's
	// own map rather than by merging them into one.
	EnabledPlugins map[string]bool `json:"enabledPlugins"`

	// ExtraKnownMarketplaces declares marketplaces beyond the ones Claude Code
	// knows natively. Read for one reason only: a marketplace sourced from a
	// local DIRECTORY is loaded from that directory and never lands in the
	// cache, so a plugin from one is unresolvable without it. Measured against
	// both real Claude Code 2.1.218 and the e2e mock, which both behave this
	// way — a project developing a plugin from its own tree is the ordinary
	// case, not an exotic one.
	ExtraKnownMarketplaces map[string]marketplace `json:"extraKnownMarketplaces"`
}

// marketplace is a marketplace declaration, in the one field that affects where
// a plugin's files are.
type marketplace struct {
	Source struct {
		Source string `json:"source"`
		Path   string `json:"path"`
	} `json:"source"`
}

// Plugin identifies one installed plugin, split from its `<plugin>@<marketplace>`
// settings key.
type Plugin struct {
	// Name is the plugin's own name — the half a guardrail is attributed to,
	// since a user who installed `sloprail` thinks of it as `sloprail` and not
	// as `sloprail@sloprail-marketplace`.
	Name string

	// Marketplace is where it came from, kept because it is half of the cache
	// path and because two marketplaces may ship a plugin of the same name.
	Marketplace string
}

// Key renders the plugin the way a settings file names it.
func (p Plugin) Key() string { return p.Name + "@" + p.Marketplace }

// parseKey splits a `<plugin>@<marketplace>` settings key.
//
// A key without an `@` is not a plugin reference this package can act on. It is
// returned as unparseable rather than guessed at, because guessing would mean
// inventing a marketplace name and then failing to find a directory under it —
// reporting a confusing path instead of the real fault, which is the key.
func parseKey(key string) (Plugin, bool) {
	at := strings.LastIndex(key, "@")
	if at <= 0 || at == len(key)-1 {
		return Plugin{}, false
	}
	return Plugin{Name: key[:at], Marketplace: key[at+1:]}, true
}

// Unresolved is an enabled plugin whose files could not be found.
//
// # Why this type exists at all
//
// This is the whole safety argument for reading a harness's configuration from
// inside the engine. Every assumption in this file can go stale: the cache can
// move, the manifest schema can go to version 3, a version directory can be
// pruned by a cleanup. What must never happen is that a stale assumption reads
// as "this project has no plugins", because that is indistinguishable from a
// project that really has none — and the guardrails simply stop firing while
// everything continues to look correct.
//
// So the two are made distinguishable at the source. A settings file naming an
// enabled plugin is the project SAYING it installed something. If the files
// backing that statement cannot be found, sloprail has failed to resolve a
// plugin the project believes it has, and says so by name.
//
// # Why it warns rather than refuses
//
// It is reported loudly at every hook point and it does not, by itself, block
// the action. The reasoning is about who is at fault and what a refusal would
// achieve.
//
// A plugin that cannot be located is not a guardrail that failed to load —
// sloprail does not know whether it shipped any guardrails at all, and most
// plugins are skills and hooks and ship none. Refusing every action in a project
// because one unrelated plugin's directory is missing would block work over a
// rule that may not exist, and the user's only remedy would be to uninstall a
// plugin they wanted. That trades a silent failure for a loud one that is
// usually wrong, and users route around tools that block them for reasons that
// turn out not to apply.
//
// The distinction being preserved is: a guardrail that EXISTS and cannot be
// checked must refuse, because reading it as approval is a lie about a rule the
// project relies on — that is refuseForBroken, and it is unchanged. A plugin
// that cannot be FOUND is a report, because nothing is yet known to have been
// relied upon. What makes the report sufficient is that it is not silent: it
// names the plugin, the key, and every path that was tried, at every hook point,
// so a user whose guardrails vanished sees why on the next tool call rather than
// never.
type Unresolved struct {
	// Plugin is the plugin that could not be located; Key is how the settings
	// file named it, kept verbatim so a user can search for the exact string
	// even when it did not parse.
	Plugin Plugin
	Key    string

	// Tried is every path that was checked, in order. This is what turns the
	// report into a diagnosis: a user seeing the cache path they expected, with
	// a version directory that is not there, knows immediately that the plugin
	// needs reinstalling — and a MAINTAINER seeing a cache root that does not
	// exist at all knows the layout assumption in this file has moved.
	Tried []string

	// Reason says what went wrong in one clause, for the front of the message.
	Reason string
}

// Message renders an unresolved plugin for a person. One wording, here, because
// every hook point reports this and separate copies would drift.
//
// It names the harness explicitly. When this fires because a schema moved, the
// reader's next question is "which part of sloprail is out of date", and the
// answer is this file — so the message points at the layer rather than leaving
// the user to conclude their own project is misconfigured.
func (u Unresolved) Message() string {
	tried := "nothing was tried"
	if len(u.Tried) > 0 {
		tried = strings.Join(u.Tried, ", ")
	}
	return fmt.Sprintf(
		"enabled plugin %q could not be located, so any guardrails it ships are NOT enforcing: %s. "+
			"Looked in: %s. "+
			"Reinstall the plugin, or remove it from enabledPlugins if it is gone; "+
			"if the plugin is installed and this persists, sloprail's Claude Code layout assumptions are out of date.",
		u.Key, u.Reason, tried)
}

// Resolution is everything one discovery pass learned: where the enabled
// plugins are, and which ones could not be found.
type Resolution struct {
	// Roots are the installation directories of the plugins that resolved, in
	// a stable order. These are what the guardrail store reads.
	Roots []Root

	// Unresolved are the enabled plugins whose directories were not found. Never
	// empty-by-omission: a plugin either lands here or in Roots.
	Unresolved []Unresolved
}

// Root is one resolved plugin installation.
type Root struct {
	Plugin Plugin

	// Dir is the directory the plugin's files are in — the one holding its
	// guardrails/, hooks/ and skills/.
	Dir string
}

// Resolve reads a project's enabled plugins and locates each one.
//
// projectDir is the project root — the directory holding `.claude/`. home is
// the user's home directory, taken as an argument rather than read from the
// environment so a test can point it at a fixture without mutating a global
// that every other test is reading concurrently.
//
// A project with no settings files, or with settings that enable nothing, is not
// an error: it is the ordinary state of most projects, and returns an empty
// Resolution. What is never done is returning empty because something could not
// be PARSED — see below.
func Resolve(projectDir, home string) (Resolution, error) {
	enabled, marketplaces, err := resolveEnabled(projectDir)
	if err != nil {
		return Resolution{}, err
	}

	var res Resolution
	for _, key := range enabled {
		plugin, ok := parseKey(key)
		if !ok {
			// A key that is not `<plugin>@<marketplace>`. Reported rather than
			// skipped, for the reason every unresolvable plugin is: the project
			// enabled something, and sloprail is not loading it.
			res.Unresolved = append(res.Unresolved, Unresolved{
				Key:    key,
				Reason: "the settings key is not in `<plugin>@<marketplace>` form",
			})
			continue
		}

		dir, tried, err := locate(plugin, marketplaces, home)
		if err != nil {
			res.Unresolved = append(res.Unresolved, Unresolved{
				Plugin: plugin, Key: key, Tried: tried, Reason: err.Error(),
			})
			continue
		}
		res.Roots = append(res.Roots, Root{Plugin: plugin, Dir: dir})
	}

	// Sorted by the settings key, which is stable across runs and independent of
	// map iteration order. Order matters downstream — it decides which plugin's
	// rule shadows another's when two ship the same name — so it must be
	// deterministic, and a user must be able to predict it. The key is the only
	// thing they can see.
	sort.Slice(res.Roots, func(i, j int) bool {
		return res.Roots[i].Plugin.Key() < res.Roots[j].Plugin.Key()
	})
	sort.Slice(res.Unresolved, func(i, j int) bool { return res.Unresolved[i].Key < res.Unresolved[j].Key })
	return res, nil
}

// resolveEnabled reads the settings files and returns the keys that are enabled
// after precedence, plus the marketplaces they declare.
//
// # The precedence, measured
//
// Claude Code 2.1.218, driven with a probe plugin whose SessionStart hook
// touches a file, across the two project-level files:
//
//	settings.json   settings.local.json   plugin runs?
//	true            false                 no
//	false           true                  YES
//	absent          true                  YES
//	true            absent                YES
//	false           absent                no
//	absent          false                 no
//
// So settings.local.json wins whenever it names the plugin, in BOTH directions —
// it can switch a plugin off that settings.json switched on, and back on again.
// It is a true override layer and not merely an additive one, which is why this
// is a per-key last-writer-wins merge rather than a union of enabled sets. A
// union would have made row 1 enable the plugin, and the personal layer would
// have been unable to switch anything off.
//
// (The same run also measured the user-level ~/.claude/settings.json, which
// loses to both: user=true with project=false does not run. It is not read here
// because a plugin enabled for the USER rather than for this project is not a
// statement this project made, and sloprail's subject is the repo. That is a
// deliberate scope choice, and it is the one place this package knowingly reads
// less than the harness does.)
func resolveEnabled(projectDir string) ([]string, map[string]marketplace, error) {
	// The verdict per key, overwritten by each successive file. Absent stays
	// absent: a file that does not mention a plugin is not an opinion about it.
	verdict := make(map[string]bool)
	// Insertion order, so the result does not depend on map iteration.
	var order []string
	marketplaces := make(map[string]marketplace)

	for _, name := range SettingsFiles {
		path := filepath.Join(projectDir, ".claude", name)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			// A settings file that exists and cannot be READ is refused rather
			// than skipped. Skipping it would silently drop every plugin it
			// enabled, which is precisely the failure this package is built to
			// make impossible — and a permissions accident would look exactly
			// like a project that had installed nothing.
			return nil, nil, fmt.Errorf("harness: read %s: %w", path, err)
		}

		var s settings
		if err := json.Unmarshal(data, &s); err != nil {
			// Same reasoning, and more sharply: a settings file with a syntax
			// error is a project stating something nobody can read. Carrying on
			// with none of its plugins would disable guardrails on account of a
			// stray comma.
			return nil, nil, fmt.Errorf("harness: parse %s: %w", path, err)
		}

		for key, on := range s.EnabledPlugins {
			if _, seen := verdict[key]; !seen {
				order = append(order, key)
			}
			verdict[key] = on
		}
		for name, m := range s.ExtraKnownMarketplaces {
			marketplaces[name] = m
		}
	}

	var enabled []string
	for _, key := range order {
		if verdict[key] {
			enabled = append(enabled, key)
		}
	}
	return enabled, marketplaces, nil
}

// locate finds one plugin's installation directory, returning every path it
// tried so a failure can say where it looked.
//
// The order below is the order Claude Code itself resolves in, and each step is
// answerable.
func locate(p Plugin, marketplaces map[string]marketplace, home string) (string, []string, error) {
	var tried []string

	// 1. A marketplace sourced from a local DIRECTORY is loaded from that
	//    directory and never enters the cache at all — measured on Claude Code
	//    2.1.218, where a plugin from such a marketplace reports a
	//    CLAUDE_PLUGIN_ROOT inside the source tree, and on the e2e mock, which
	//    leaves its cache directory empty for the same case.
	//
	//    This is checked FIRST because when it applies, the cache holds nothing
	//    to find, and a cache-first order would report every locally-developed
	//    plugin as unresolvable. It is also the case a plugin AUTHOR is always
	//    in, so getting it wrong would break the people most likely to notice.
	if m, ok := marketplaces[p.Marketplace]; ok && m.Source.Source == "directory" && m.Source.Path != "" {
		// The plugin's own subdirectory within the marketplace, then the
		// marketplace root itself — a single-plugin marketplace commonly IS the
		// plugin, which is the shape this repo's own marketplace uses.
		for _, dir := range pluginDirsInMarketplace(m.Source.Path, p.Name) {
			tried = append(tried, dir)
			if isDir(dir) {
				return dir, tried, nil
			}
		}
	}

	// 2. The install cache, `<cache>/<marketplace>/<plugin>/<version>/`.
	cacheRoot := CacheRoot(home)
	pluginCache := filepath.Join(cacheRoot, p.Marketplace, p.Name)
	if !isDir(pluginCache) {
		tried = append(tried, pluginCache)
		return "", tried, fmt.Errorf("no installation directory")
	}

	version, err := liveVersion(p, pluginCache, home)
	if err != nil {
		tried = append(tried, pluginCache)
		return "", tried, err
	}
	dir := filepath.Join(pluginCache, version)
	tried = append(tried, dir)
	if !isDir(dir) {
		return "", tried, fmt.Errorf("the recorded version %q is not installed", version)
	}
	return dir, tried, nil
}

// pluginDirsInMarketplace lists where a plugin's files may sit within a
// directory-sourced marketplace, in the order to try them.
func pluginDirsInMarketplace(root, name string) []string {
	return []string{
		// The layout this repo uses and the one the marketplace manifest's
		// relative `source` fields point into.
		filepath.Join(root, "marketplace", "plugins", name),
		filepath.Join(root, "plugins", name),
		filepath.Join(root, name),
		// A marketplace that is itself one plugin.
		root,
	}
}

// CacheRoot is where Claude Code unpacks installed plugins.
//
// Overridable through CLAUDE_CODE_PLUGIN_CACHE_DIR, which is the variable Claude
// Code itself honours and the one the e2e harness sets — so a test's plugins are
// found by the same code path a user's are, rather than by a test-only branch.
// A test-only branch here would mean the mechanism under test is not the
// mechanism that ships.
func CacheRoot(home string) string {
	if dir := os.Getenv("CLAUDE_CODE_PLUGIN_CACHE_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".claude", "plugins", "cache")
}

// installedPlugins is the fragment of installed_plugins.json this package reads.
//
// Version is checked, not ignored. The file declares `"version": 2` today, and
// reading a version 3 file with a version 2 parser is how a silent
// misinterpretation happens — fields that moved would decode as zero values and
// the plugin would resolve to the wrong directory, or to none, without complaint.
// An unrecognised version is treated as "this file cannot be trusted", which
// falls through to the directory listing below rather than failing outright.
type installedPlugins struct {
	Version int                          `json:"version"`
	Plugins map[string][]installedRecord `json:"plugins"`
}

// installedRecord is one installation of one plugin.
type installedRecord struct {
	Version     string `json:"version"`
	InstallPath string `json:"installPath"`
}

// installedPluginsSchema is the version of installed_plugins.json this package
// knows how to read.
const installedPluginsSchema = 2

// liveVersion decides which version directory is the live one.
//
// # Why the manifest is read at all
//
// The tempting simplification is to skip installed_plugins.json entirely and
// take the only directory under `<marketplace>/<plugin>/`, which would remove a
// schema assumption. It was measured and it is not sound: on this machine
// `a10n-spec-capability` has both 0.0.1 and 0.0.8 in the cache, and `figma` has
// both 2.2.50 and 2.2.81, while the manifest records 0.0.8 and 2.2.81 as live.
// Claude Code leaves superseded versions behind on upgrade. Listing alone would
// therefore pick a stale version roughly half the time, and mtime does not
// break the tie either — both figma directories carry the same timestamp to the
// minute.
//
// Picking a stale version is worse than it sounds: the guardrails would load and
// fire, from an old copy, and nothing would look wrong. So the manifest is read
// when there is a choice to make.
//
// # Why it is not required
//
// The manifest is consulted, never depended upon. When it is missing, unreadable,
// of an unknown schema version, or silent about this plugin, resolution falls
// back to the directory listing — which is exact when there is only one version,
// the overwhelmingly common case. The schema assumption therefore degrades to a
// wrong-version risk for multi-version plugins rather than to total blindness,
// and total blindness is the failure that mattered.
func liveVersion(p Plugin, pluginCache, home string) (string, error) {
	versions, err := versionDirs(pluginCache)
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no version directory is installed")
	}

	// One version: nothing to disambiguate, and the manifest cannot improve on
	// it. Read nothing.
	if len(versions) == 1 {
		return versions[0], nil
	}

	if v, ok := recordedVersion(p, home); ok {
		for _, have := range versions {
			if have == v {
				return v, nil
			}
		}
		// The manifest names a version that is not on disk. The listing is the
		// better answer, since it at least names something that exists.
	}

	// No usable record and more than one candidate. The highest version is the
	// best guess available — Claude Code leaves the OLD one behind on upgrade,
	// so the newest is the one it installed most recently.
	return versions[len(versions)-1], nil
}

// recordedVersion reads the live version out of installed_plugins.json.
//
// Every failure returns false rather than an error: this file is an optimisation
// over the directory listing, and a caller that cannot read it has a worse
// answer rather than no answer. That is the property that keeps a schema move
// from being fatal.
func recordedVersion(p Plugin, home string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	if err != nil {
		return "", false
	}
	var ip installedPlugins
	if err := json.Unmarshal(data, &ip); err != nil {
		return "", false
	}
	if ip.Version != installedPluginsSchema {
		// A schema this package was not written against. Refusing to interpret
		// it is the point: the fallback is merely less precise, whereas
		// misreading it would be confidently wrong.
		return "", false
	}
	records := ip.Plugins[p.Key()]
	if len(records) == 0 {
		return "", false
	}
	// The last record wins. Claude Code appends per scope, and the versions
	// agree in every case observed; taking the last is stable and never returns
	// a version the file did not name.
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Version != "" {
			return records[i].Version, true
		}
	}
	return "", false
}

// versionDirs lists the version directories under a plugin's cache entry, in
// ascending version order.
func versionDirs(pluginCache string) ([]string, error) {
	entries, err := os.ReadDir(pluginCache)
	if err != nil {
		return nil, fmt.Errorf("cannot list installed versions: %w", err)
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() {
			versions = append(versions, e.Name())
		}
	}
	sort.Slice(versions, func(i, j int) bool { return lessVersion(versions[i], versions[j]) })
	return versions, nil
}

// lessVersion orders two version directory names, comparing numeric components
// numerically so that 0.0.10 sorts above 0.0.9 rather than below it.
//
// A plain string sort is wrong here in a way that only shows up after ten
// releases, which is exactly the kind of fault that ships. Non-numeric
// components fall back to a string comparison.
func lessVersion(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		an, aerr := atoiStrict(as[i])
		bn, berr := atoiStrict(bs[i])
		if aerr || berr {
			return as[i] < bs[i]
		}
		return an < bn
	}
	return len(as) < len(bs)
}

// atoiStrict parses a non-negative integer, reporting failure rather than a
// partial parse. The bool is `bad` so the zero value means success.
func atoiStrict(s string) (int, bool) {
	if s == "" {
		return 0, true
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, true
		}
		n = n*10 + int(r-'0')
	}
	return n, false
}

// isDir reports whether a path exists and is a directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
