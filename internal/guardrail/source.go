package guardrail

import (
	"os"
	"path/filepath"
	"strings"
)

// PluginDirsEnv names the plugin installations whose guardrails this engine
// should load, as a colon-separated list of directories.
//
// # Why an environment variable, and why this one
//
// A guardrail that ships inside a tool has to be FOUND, and the only thing that
// knows which tools are installed is the harness. Claude Code writes that down
// in ~/.claude/plugins/installed_plugins.json, and reading that file from here
// is the one thing this design refuses: it would make the engine's discovery
// path a function of which harness it happens to be running under, and every
// other harness a special case someone has to add. The engine would then know
// three things about Claude Code — the manifest's path, its JSON shape, and its
// cache layout — none of which is any of the engine's business.
//
// So the knowledge is inverted. The engine declares a variable it will read;
// the harness-specific layer fills it in. That layer already exists and is
// already harness-specific by construction: the PLUGIN's own hooks.json is
// Claude Code's format, maps Claude Code's lifecycle names, and is installed by
// Claude Code's marketplace. It is the correct and only place for Claude
// Code-specific knowledge, and it is where this value comes from —
// `${CLAUDE_PLUGIN_ROOT}`, which Claude Code substitutes into a plugin hook's
// command string before running it (measured: it is a TEMPLATE substitution,
// not an exported environment variable, so a hook has to pass it along
// explicitly — see marketplace/plugins/sloprail/hooks/hooks.json).
//
// The test of whether this is really harness-agnostic is not that the name is
// generic. It is that a second harness needs no change HERE: a plugin layer for
// any other tool sets the same variable to wherever that tool unpacked its
// plugins, and every mechanism below — precedence, disabling, attribution —
// works unchanged. Nothing in this package can tell which harness filled it in,
// and that is the property being bought.
//
// COLON-separated, matching PATH and SLOPRAIL_LAUNCHED_BY, because a list of
// directories in this environment is already spelled that way.
const PluginDirsEnv = "SR_PLUGIN_DIRS"

// Origin says where a guardrail was found, which is the difference between a
// rule the project wrote and a rule it installed.
//
// It exists because a refusal has to be actionable. "guardrail X refused this"
// sends an author to .sloprail/guardrails/X, and for a plugin's rule there is
// nothing there — the file lives in a cache directory the project has never
// looked at and did not write. A user meeting a rule they cannot find is a user
// who concludes the tool is broken, and the honest fix is for the rule to say
// where it came from.
type Origin struct {
	// Plugin is the name of the plugin this guardrail shipped inside, or "" for
	// a guardrail the project declared itself.
	//
	// Taken from the installation directory's own name rather than from any
	// manifest inside it. The engine must not parse a harness's plugin metadata
	// — that format is the harness's, and a parser for it here is the coupling
	// PluginDirsEnv exists to avoid. The directory name is what every harness
	// has in common.
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

// PluginDirs reads the plugin installation directories from the environment.
//
// Read through a lookup rather than os.Getenv directly, so a test can supply one
// without mutating the process environment — which under -race would race every
// other test reading one. The same convention internal/guardrail's sibling
// mechanisms already follow.
//
// Entries are returned in the order given, with blanks dropped. Order is not
// cosmetic: it decides which plugin's rule shadows another's when two ship the
// same name, so it must be the caller's to control and must not be sorted here.
func PluginDirs(getenv func(string) string) []string {
	raw := getenv(PluginDirsEnv)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, dir := range strings.Split(raw, string(os.PathListSeparator)) {
		if dir = strings.TrimSpace(dir); dir != "" {
			out = append(out, dir)
		}
	}
	return out
}

// pluginName is the name a plugin installation is known by: its directory's own
// name, with a version component skipped when the layout carries one.
//
// Claude Code installs to <marketplace>/<plugin>/<version>/, so the leaf is a
// version string and the name is its parent. Rather than encode that layout —
// which is exactly the harness knowledge this file refuses to hold — the leaf is
// used unless it LOOKS like a version, which is a property of the string itself
// and true of any harness that versions its installs the ordinary way.
//
// A wrong guess here costs a confusing name in a refusal, never a rule that
// fails to load or one that loads when it should not: the name is used for
// attribution and for the disable list, both of which the consumer reads off the
// engine's own report rather than deriving themselves.
func pluginName(root string) string {
	clean := filepath.Clean(root)
	base := filepath.Base(clean)
	if looksLikeVersion(base) {
		if parent := filepath.Base(filepath.Dir(clean)); parent != "." && parent != string(filepath.Separator) {
			return parent
		}
	}
	return base
}

// looksLikeVersion reports whether a path component is a version number rather
// than a name — "0.0.1", "2.1.222", "1.0". Digits and dots only, with at least
// one dot, which no ordinary plugin name is and every ordinary version is.
func looksLikeVersion(s string) bool {
	if s == "" || !strings.ContainsRune(s, '.') {
		return false
	}
	for _, r := range s {
		if r != '.' && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
