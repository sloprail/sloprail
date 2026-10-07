package checkrun

import (
	"fmt"
	"io"
	"os"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/module"
)

// LoadDeclarations loads the new-format declarations in force for a folder: the project's
// own under `.sloprail`, PLUS those shipped by the plugins the project has enabled. Any that
// could not be loaded are reported on w, as are unresolved plugins and shadowed declarations.
//
// Plugin resolution goes through internal/harness: it reads the project's own
// `.claude/settings.json` and `settings.local.json`, locates each enabled plugin's
// installation, and returns its root. Every failure is REPORTED and treated as "no plugin
// declarations", never as a refusal: a broken settings file must not disarm the project's own
// rules, and a plugin whose files were not found is named loudly rather than silently absent.
// An unreadable store is reported and treated as empty: a directory that gives the engine
// nothing to enforce must not make it refuse every action the agent cannot fix.
//
// sessionStart, when given, is the commit whose config.yaml may switch off a protected rule.
// A registry is required so trigger matches can be evaluated.
func LoadDeclarations(w io.Writer, cwd string, reg *module.Registry, sessionStart ...string) declaration.Loaded {
	loaded, err := LoadDeclarationsStrict(w, cwd, reg, sessionStart...)
	if err != nil {
		fmt.Fprintf(w, "sloprail: the new-format declarations in this project could not be read: %v\n", err)
		return declaration.Loaded{}
	}
	return loaded
}

// LoadDeclarationsStrict is LoadDeclarations that returns a store that cannot be READ (an I/O or
// permission error, not an absent `.sloprail`) as an error instead of an empty set. A CLI
// `run`/`verify` must refuse on it: seeing zero guards there would read as a green CI.
func LoadDeclarationsStrict(w io.Writer, cwd string, reg *module.Registry, sessionStart ...string) (declaration.Loaded, error) {
	store, unresolved := DeclarationStore(w, cwd)
	if len(sessionStart) > 0 {
		store.WithTrustedRev(sessionStart[0])
	}
	ReportUnresolved(w, unresolved)

	loaded, err := store.Load(reg)
	if err != nil {
		return declaration.Loaded{}, err
	}
	ReportNatureInvalid(w, loaded.Invalid)
	ReportNatureDegraded(w, loaded.Degraded)
	for _, sh := range loaded.Shadowed {
		fmt.Fprintf(w, "sloprail: %s\n", sh.Message())
	}
	for _, o := range loaded.ScopeOverlaps {
		fmt.Fprintf(w, "sloprail: %s\n", o.Message())
	}
	return loaded, nil
}

// DeclarationStore builds the plugin-aware declaration store for a folder, resolving the
// enabled plugins through internal/harness, and returns the unresolved plugins alongside so
// the caller can report them. On any resolution failure it returns a PROJECT-ONLY store (the
// project's own `.sloprail`, no plugins) rather than nil, so the project's own rules keep
// enforcing even when plugin discovery could not run.
func DeclarationStore(w io.Writer, cwd string) (*declaration.Store, []harness.Unresolved) {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(w, "sloprail: plugin-shipped new-format declarations not loaded (no home directory to locate the plugin cache): %v\n", err)
		return declaration.New(DotDir(cwd)), nil
	}
	res, err := harness.Current().ResolvePlugins(ProjectDir(cwd), home)
	if err != nil {
		fmt.Fprintf(w, "sloprail: plugin-shipped new-format declarations not loaded (the project's plugin settings could not be read): %v\n", err)
		return declaration.New(DotDir(cwd)), nil
	}
	plugins := make([]declaration.Origin, 0, len(res.Roots))
	for _, r := range res.Roots {
		plugins = append(plugins, declaration.Origin{Plugin: r.Plugin.Name, Root: r.Dir})
	}
	return declaration.NewWithPlugins(DotDir(cwd), plugins), res.Unresolved
}

// ReportUnresolved names every enabled plugin whose files could not be found, so a moved
// cache layout or a bumped manifest schema cannot silently disable a plugin's shipped rules.
func ReportUnresolved(w io.Writer, unresolved []harness.Unresolved) {
	for _, u := range unresolved {
		fmt.Fprintf(w, "sloprail: %s\n", u.Message())
	}
}

// ReportNatureInvalid names every declaration that could not be loaded, one line per fault,
// then how to get unstuck. Reported rather than fatal: a broken declaration blocks nothing,
// but is named every time so an author fixing it sees all of it.
func ReportNatureInvalid(w io.Writer, invalid []declaration.Invalid) {
	for _, iv := range invalid {
		fmt.Fprintf(w, "sloprail: declaration %s not loaded:\n", iv.Attribution())
		for _, reason := range iv.Reasons {
			fmt.Fprintf(w, "  - %s\n", reason)
		}
		fmt.Fprintf(w, "  %s\n", iv.Remedy())
	}
}

// ReportNatureDegraded names every declaration that IS loaded and enforced but whose declared
// script cannot be exec'd (it lost its shebang or execute bit), with the file and the fix. The
// rule keeps refusing what it guards (the exec path refuses with the same error) until the file
// is fixed, so unlike an unloadable declaration there is no gap to close, only a repair to make.
func ReportNatureDegraded(w io.Writer, degraded []declaration.Invalid) {
	for _, iv := range degraded {
		fmt.Fprintf(w, "sloprail: declaration %s is loaded and enforced, but a script it declares cannot run, so it REFUSES what it guards until fixed:\n", iv.Attribution())
		for _, reason := range iv.Reasons {
			fmt.Fprintf(w, "  - %s\n", reason)
		}
	}
}
