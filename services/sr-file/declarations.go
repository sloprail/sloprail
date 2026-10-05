package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/module/modules"
)

// `sr-file declarations <dir>` loads the new-format declarations under a
// project's `.sloprail` directory and reports what loaded and what did not.
//
// It is a DEBUG/inspection surface over internal/declaration's loader, not part
// of the live hook dispatch — the dispatch slice that actually matches events
// against these declarations and runs their checks is a later batch. What this
// gives now is a way to point sloprail at a `.sloprail` tree from the shell and
// see, before any dispatch exists, that every declaration parses and validates:
// which file-guards, gates, contexts and structure gate loaded, and for any that
// did not, the exact faults by name.
//
// It lives on sr-file because sr-file is "checks over a file's contents", and
// checking that a project's declaration files are well-formed is the same family
// of job as checking one file against a schema — the command sr-file already
// hosts. Reached as `sr file declarations <dir>` through the proxy, or
// `sr-file declarations <dir>` directly.
//
// EXIT STATUS mirrors `validate`: 0 when every declaration loaded, 1 when any was
// invalid — so a hook or a CI step can read the status. The per-declaration
// faults are printed in the form a person reads, one line each, the same shape a
// refusal forwards.

// newDeclarationsCmd builds `sr-file declarations <dir>`.
//
// The single positional argument is the directory to load. It defaults to
// interpreting the given path AS the `.sloprail` root when it already ends in
// `.sloprail`, and otherwise as a project root whose `.sloprail` subdirectory is
// loaded — so both `sr-file declarations .` (a project root) and `sr-file
// declarations ./.sloprail` name the same tree, and an author does not have to
// remember which the command wanted.
func newDeclarationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "declarations <dir>",
		Short: "Load and validate the .sloprail declarations under a directory",
		Long: "Load and validate the new-format declarations a project keeps under its\n" +
			".sloprail directory — file-guards, gates, contexts, and the structure\n" +
			"gate — reporting what loaded and, for anything that did not, the faults by name.\n\n" +
			"This is an inspection surface over the declaration loader, not part of the live\n" +
			"hook dispatch: it proves a project's declaration files parse and validate before\n" +
			"any event is dispatched against them.\n\n" +
			"THE ARGUMENT is a directory. A path ending in .sloprail is loaded as the root\n" +
			"itself; any other path is treated as a project root whose .sloprail subdirectory\n" +
			"is loaded — so `declarations .` and `declarations ./.sloprail` name the same tree.\n\n" +
			"--plugin NAME loads the directory as the root of an installed plugin named NAME\n" +
			"instead of a project, validating what a plugin ships by the plugin rules — a\n" +
			"plugin's structure gate must declare a `scope`, a project's must not.\n\n" +
			"STRUCTURE GATES are listed one per root: the project's (the whole tree) and each\n" +
			"plugin's with the scope it owns.\n\n" +
			"EXIT STATUS is 0 when every declaration loaded and 1 when any was invalid, so a\n" +
			"hook or a CI step can read it. Invalid declarations are printed one fault per line.\n\n" +
			"EXAMPLES:\n" +
			"  sr-file declarations .\n" +
			"  sr-file declarations examples/eval-loop-maxing\n" +
			"  sr-file declarations examples/eval-loop-maxing/.sloprail\n" +
			"  sr-file declarations --plugin mdmap path/to/plugins/mdmap",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runDeclarations,
	}
	cmd.Flags().String("plugin", "", "load the directory as the root of the plugin with this name, not a project")
	return cmd
}

// dotDirName is the directory a project keeps its declarations under.
const dotDirName = ".sloprail"

// resolveRoot turns the command's directory argument into the `.sloprail` root to
// load. A path already ending in `.sloprail` is taken as-is; anything else is a
// project root whose `.sloprail` subdirectory is the root.
func resolveRoot(arg string) string {
	if filepath.Base(arg) == dotDirName {
		return arg
	}
	return filepath.Join(arg, dotDirName)
}

func runDeclarations(cmd *cobra.Command, args []string) error {
	root := resolveRoot(args[0])

	// The one shipped module registry — the same vocabulary the engine enforces
	// against, so a trigger's `match` here is checked exactly as it will be at
	// dispatch. modules.Registry is the only sanctioned builder.
	reg, err := modules.Registry()
	if err != nil {
		return fmt.Errorf("sr-file declarations: %w", err)
	}

	store := declaration.New(root)
	plugin, _ := cmd.Flags().GetString("plugin")
	if plugin != "" {
		// A plugin's root is the directory HOLDING its `.sloprail`.
		store = declaration.NewPlugin(declaration.Origin{Plugin: plugin, Root: filepath.Dir(root)})
	}

	loaded, err := store.Load(reg)
	if err != nil {
		// A hard error is the loader unable to READ the tree (a permissions
		// failure on a directory), distinct from a declaration being invalid —
		// that latter is reported below, not returned here.
		return fmt.Errorf("sr-file declarations: %w", err)
	}

	out := cmd.OutOrStdout()

	// The loaded declarations, by nature, so a person sees what is in force. Names
	// only — the point of this surface is "did they load", and the detail lives in
	// the files.
	if plugin != "" {
		fmt.Fprintf(out, "Loaded from %s (as plugin %q):\n", root, plugin)
	} else {
		fmt.Fprintf(out, "Loaded from %s:\n", root)
	}
	printNature(out, "file-guards", fileGuardNames(loaded))
	printNature(out, "gates", gateNames(loaded))
	printNature(out, "contexts", contextNames(loaded))
	printStructures(out, loaded)

	if len(loaded.Invalid) == 0 && len(loaded.Degraded) == 0 {
		return nil
	}

	// Anything that did not load, with every fault. Printed to stderr in the form
	// a refusal forwards, and the command exits 1 via errRefused so main prints no
	// second line after the report.
	errOut := cmd.ErrOrStderr()
	if len(loaded.Invalid) > 0 {
		fmt.Fprintf(errOut, "\n%d declaration(s) could not be loaded:\n", len(loaded.Invalid))
		for _, iv := range loaded.Invalid {
			fmt.Fprintf(errOut, "  %s (%s):\n", iv.Qualified(), iv.Path)
			for _, reason := range iv.Reasons {
				fmt.Fprintf(errOut, "    - %s\n", reason)
			}
		}
	}
	// Loaded and enforced, but a declared script cannot run: the rule refuses what it guards
	// until the file is fixed. Still exit 1, so a CI step reading the status sees it.
	if len(loaded.Degraded) > 0 {
		fmt.Fprintf(errOut, "\n%d declaration(s) are loaded and enforced but cannot run (they refuse what they guard until fixed):\n", len(loaded.Degraded))
		for _, iv := range loaded.Degraded {
			fmt.Fprintf(errOut, "  %s (%s):\n", iv.Qualified(), iv.Path)
			for _, reason := range iv.Reasons {
				fmt.Fprintf(errOut, "    - %s\n", reason)
			}
		}
	}
	return errRefused
}

// printStructures prints every loaded structure gate, one line each: the
// project's (the whole tree) and each plugin's with the scope it owns, then any
// overlap between two plugins' literal scopes.
func printStructures(out io.Writer, l declaration.Loaded) {
	if len(l.Structures) == 0 {
		fmt.Fprintln(out, "  structure gate: none")
		return
	}
	for _, sg := range l.Structures {
		if !sg.Origin.FromPlugin() {
			fmt.Fprintf(out, "  structure gate: present (%d allow, %d deny)\n", len(sg.Allow), len(sg.Deny))
			continue
		}
		fmt.Fprintf(out, "  structure gate from plugin %q: owns %s (%d allow, %d deny)\n",
			sg.Origin.Plugin, strings.Join(sg.ScopeGlobs(), ", "), len(sg.Allow), len(sg.Deny))
	}
	for _, o := range l.ScopeOverlaps {
		fmt.Fprintf(out, "  warning: %s\n", o.Message())
	}
}

// printNature prints one nature's loaded names, or "none" when it declared none.
func printNature(out io.Writer, label string, names []string) {
	if len(names) == 0 {
		fmt.Fprintf(out, "  %s: none\n", label)
		return
	}
	fmt.Fprintf(out, "  %s (%d): ", label, len(names))
	for i, n := range names {
		if i > 0 {
			fmt.Fprint(out, ", ")
		}
		fmt.Fprint(out, n)
	}
	fmt.Fprintln(out)
}

func fileGuardNames(l declaration.Loaded) []string {
	names := make([]string, 0, len(l.FileGuards))
	for _, g := range l.FileGuards {
		names = append(names, g.Name)
	}
	return names
}

func gateNames(l declaration.Loaded) []string {
	names := make([]string, 0, len(l.Gates))
	for _, g := range l.Gates {
		names = append(names, g.Name)
	}
	return names
}

func contextNames(l declaration.Loaded) []string {
	names := make([]string, 0, len(l.Contexts))
	for _, c := range l.Contexts {
		names = append(names, c.Name)
	}
	return names
}
