package guardrail

// Origin says where a guardrail was found, which is the difference between a
// rule the project wrote and a rule it installed.
//
// It exists because a refusal has to be actionable. "guardrail X refused this"
// sends an author to .sloprail/guardrails/X, and for a plugin's rule there is
// nothing there — the file lives in an install directory the project has never
// looked at and did not write. A user meeting a rule they cannot find is a user
// who concludes the tool is broken, and the honest fix is for the rule to say
// where it came from.
//
// Note what this package does NOT do: it does not discover plugins. The set of
// installed plugins is a fact about the project's harness configuration, and it
// is read in internal/harness, which is allowed to know one harness's layout.
// This package is handed roots and origins already resolved. That separation is
// what keeps every Claude Code path and schema assumption in a single file
// instead of spread through the loader.
type Origin struct {
	// Plugin is the name of the plugin this guardrail shipped inside, or "" for
	// a guardrail the project declared itself.
	//
	// Supplied by the resolver, which read it out of the settings key that
	// enabled the plugin, rather than derived here from the directory name.
	// The settings key is what the USER wrote and what they will search for; a
	// directory name is an artefact of a cache layout they never see, and the
	// two can differ.
	Plugin string

	// Root is the plugin installation directory this guardrail was found under,
	// empty for a project's own. Kept so a diagnostic can name the actual path,
	// which is the thing an author needs when the rule is not where they would
	// look.
	Root string
}

// FromPlugin reports whether this guardrail came from an installed plugin
// rather than from the project itself.
func (o Origin) FromPlugin() bool { return o.Plugin != "" }

// Qualified is the guardrail's name as a consumer must write it to disable it:
// `<plugin>/<guardrail>` for a plugin's rule, and the bare name for a project's.
//
// A plugin's rules are namespaced because two plugins may reasonably ship a
// rule called `no-secrets`, and a disable list that could not tell them apart
// would switch off a rule the consumer did not mean. The project's own rules
// are NOT namespaced, because they already live in one directory and cannot
// collide.
func (o Origin) Qualified(name string) string {
	if !o.FromPlugin() {
		return name
	}
	return o.Plugin + "/" + name
}

// Describe renders the origin for a person reading a refusal or a diagnostic.
//
// Empty for a project's own rule, deliberately: a project author reading their
// own rule's name already knows where it is, and appending "(from this project)"
// to every refusal is noise that trains people to stop reading refusals.
func (o Origin) Describe() string {
	if !o.FromPlugin() {
		return ""
	}
	return " from plugin " + quote(o.Plugin)
}
