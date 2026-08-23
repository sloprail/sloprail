package declaration

// This file gives a new-format declaration the same provenance the old format
// carries on internal/guardrail's Origin: the difference between a rule a project
// wrote and a rule it installed. It is a DELIBERATE MIRROR of guardrail.Origin,
// not a reuse of it.
//
// # Why mirror rather than import guardrail.Origin
//
// This package still imports internal/guardrail — but only for the match
// COMPILERS (CompileFileMatch and friends), an implementation detail of
// validation. Its PUBLIC types are its own: Invalid, Problem, Nature are all
// defined here rather than borrowed, precisely because internal/declaration is
// the format that OUTLIVED internal/guardrail's old types. Those old types
// (guardrail.Origin/Declaration/Invalid/Config/Problem) have since been deleted;
// a NewWithPlugins whose signature had named guardrail.Origin would have taken
// that deletion down with it, and the loader that was meant to be the survivor
// would have depended on the corpse. So Origin joins Invalid/Problem/Nature as a
// type this package owns, and the caller in services/sr-session builds a
// declaration.Origin from the harness resolution directly — one construction at
// the boundary, no shared type across it. The match compilers survive in
// internal/guardrail (matcher.go/scopes.go), which is all this import now reaches.

// Origin says where a declaration was found, which is the difference between a
// rule the project wrote and a rule it installed.
//
// It exists for the same reason guardrail.Origin does: a refusal has to be
// actionable. "file-guard X refused this" sends an author to
// .sloprail/file-guard/X, and for a plugin's rule there is nothing there — the
// file lives in an install directory the project never wrote to. A user meeting
// a rule they cannot find is a user who concludes the tool is broken, and the
// honest fix is for the rule to say where it came from.
//
// Note what this type does NOT do: it does not discover plugins. The set of
// installed plugins is a fact about the project's harness configuration, read in
// internal/harness, which is allowed to know one harness's layout. This package
// is handed roots and origins already resolved — the same division guardrail.Origin
// documents and the same one the harness package's doc insists on.
type Origin struct {
	// Plugin is the name of the plugin this declaration shipped inside, or "" for
	// a declaration the project declared itself.
	//
	// Supplied by the resolver, which read it out of the settings key that enabled
	// the plugin, rather than derived here from the directory name. The settings
	// key is what the USER wrote and what they will search for; a cache directory's
	// name is an artefact they never see, and the two can differ. (This is the
	// exact reasoning guardrail.Origin.Plugin records.)
	Plugin string

	// Root is the plugin installation directory this declaration was found under,
	// empty for a project's own. Kept so a diagnostic can name the actual path,
	// which is what an author needs when the rule is not where they would look.
	Root string
}

// FromPlugin reports whether this declaration came from an installed plugin
// rather than from the project itself.
func (o Origin) FromPlugin() bool { return o.Plugin != "" }

// Qualified is the declaration's name as a consumer must write it to disable it.
//
// It namespaces on BOTH the plugin AND the nature, `<plugin>/<nature>/<name>`
// for a plugin's rule, `<nature>/<name>` for a project's own. Two axes, not one,
// because the new format identifies a declaration by (nature, name) — a gate and
// a context may both be named `people-linked`, so guardrail.Origin's
// `<plugin>/<name>` would be ambiguous here in a way it never is for the old
// format's single flat guardrails/ directory. The nature is therefore part of
// the disable key, mirroring how Invalid.Qualified already keys `<nature>/<name>`
// for a project's own declarations; the plugin prefix is the FromPlugin addition,
// mirroring guardrail.Origin.Qualified.
//
// A project's own declarations are namespaced by nature but not by plugin,
// because they already live under one `.sloprail` and cannot collide across
// installs. A plugin's are namespaced by both, because two plugins may reasonably
// ship a file-guard called `no-secrets` and a disable list that could not tell
// them apart would switch off a rule the consumer did not mean.
func (o Origin) Qualified(nature Nature, name string) string {
	// The structure singleton has no per-name folder, so its base is the bare
	// nature — the same shape Invalid.Qualified uses for an empty name, so the two
	// keys agree for the one declaration that has no name.
	base := string(nature)
	if name != "" {
		base += "/" + name
	}
	if !o.FromPlugin() {
		return base
	}
	return o.Plugin + "/" + base
}

// Describe renders the origin for a person reading a refusal or a diagnostic.
//
// Empty for a project's own rule, deliberately — the same choice guardrail.Origin
// makes: a project author reading their own rule's name already knows where it
// is, and appending "(from this project)" to every refusal is noise that trains
// people to stop reading refusals. For a plugin's rule it names the plugin, which
// is the fact a user needs to find a file that is not in their tree.
func (o Origin) Describe() string {
	if !o.FromPlugin() {
		return ""
	}
	return " from plugin " + quoteName(o.Plugin)
}

// quoteName wraps a name in double quotes for a message. A local helper rather
// than a reach into internal/guardrail's unexported quote — this package spells
// its own diagnostics, and borrowing an unexported helper across the boundary is
// exactly the coupling source.go's header argues against.
func quoteName(s string) string { return `"` + s + `"` }
