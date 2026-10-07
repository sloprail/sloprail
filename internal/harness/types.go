// Package harness is the harness-neutral seam: the interface every coding-agent
// harness (Claude Code today; Codex or Cursor later) implements, and the shared
// normalized types its callers consume. Everything that knows one harness's wire
// formats, files or environment lives in a sibling package, internal/harness/<name>/,
// and only a service's main package chooses which one runs (see Use).
package harness

import (
	"fmt"
	"strings"
)

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

// Process is a live Claude Code process as ~/.claude/sessions/<pid>.json records it: its pid and
// the opaque start marker the harness wrote beside it, which tells a pid reused by another
// process from the one that was recorded.
type Process struct {
	PID       int
	ProcStart string
}
