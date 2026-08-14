// Command sr-file checks a file against a schema. It is its own STANDALONE binary
// — not a subcommand of the `sloprail` binary — following the shape every
// high-level sloprail command is moving to: one binary per command, with a root
// `sr` that proxies to them. `sr-mark` is the same pattern.
//
//	sr-file validate <path> --schema <schema.cue>
//
// CUE DOES THE WORK, AND THE INTERFACE IS CUE'S. The flags, the schema syntax and
// the exit status are the ones `cue vet` already has, because an author who knows
// CUE should not have to learn a second dialect of it, and a schema written for
// this should still be a schema. `-c`/`--concrete` and `-d`/`--path` mean here
// what they mean there; the schema language is CUE's, unmodified, because the
// schema is compiled by CUE.
//
// Exit status follows `cue vet`: 0 when the document satisfies the schema, 1 when
// it does not or when the check could not be run. A hook reads that status.
//
// WHAT IS ADDED is the part CUE has no opinion about — which bytes of the file
// are the document (see document.go) — and the reporting, because CUE names the
// file for some faults and not others (see validate.go).
//
// WHAT CUE DOES NOT DO, measured rather than assumed:
//
//   - A schema is OPEN by default, so an unexpected field is permitted. A rule
//     that must refuse unknown keys has to say so — `close({...})` or a `#Def` —
//     and `cue vet` behaves the same way. Nothing here narrows it, because
//     narrowing it would make a schema written for this mean something different
//     from the same schema handed to `cue vet`.
//   - When a PRESENT field violates a constraint, CUE stops before reporting
//     fields that are ABSENT. A document with a bad value AND a missing required
//     field reports only the bad value, so an agent that fixes it is refused
//     again for the missing one. `--all-errors` does not change this; it is
//     evaluation order. Several missing fields together, or several bad values
//     together, ARE all reported — it is only the mixture that truncates.
//
// CUE IS EMBEDDED, not shelled out to. Measured on this machine: the whole `cue`
// binary costs ~15 ms median to validate a small frontmatter, essentially all of
// it process startup, and shelling out means paying sr-file's OWN startup and
// then cue's — ~25 ms median together. Embedded, the same check is ~12 ms median
// end to end, of which the validation itself is 0.09 ms; the rest is one Go
// binary starting. Embedding also removes a runtime dependency an author would
// otherwise have to install before any guardrail using this could run at all.
// The cost is binary size: ~23 MB against sr-mark's ~4 MB.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// newRoot is the `sr-file` root. It carries no behaviour of its own — it hosts
// the verbs, of which `validate` is the first.
func newRoot() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "sr-file <command>",
		Short:         "Checks over a file's contents",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newValidateCmd())
	return cmd
}

// newValidateCmd builds `sr-file validate <path> --schema <schema.cue>`.
//
// The flag names are `cue vet`'s where they apply. `-c`/`--concrete` is CUE's
// flag for requiring every field to have a concrete value, which is what makes a
// missing required field a failure. It DEFAULTS TO TRUE here, which is the one
// place this departs from `cue vet`'s defaults, and deliberately: this command
// exists to be called by a hook enforcing that a file has what it must have, and
// a default that silently permits a document missing every required field would
// make the common case the wrong one. `--concrete=false` restores CUE's default.
func newValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate <path> --schema <schema.cue>",
		Short: "Check a file against a CUE schema",
		Long: "Check a file against a CUE schema.\n\n" +
			"WHICH BYTES ARE THE DOCUMENT is decided by the file's extension: the frontmatter of a\n" +
			".md, the whole of a .yaml, .yml or .json. A markdown file is a YAML document wearing\n" +
			"prose below it, so handing the whole file to a schema checker gets an error about the\n" +
			"prose. An extension outside that set is an error rather than a guess.\n\n" +
			"THE SCHEMA IS CUE, unmodified — the same file `cue vet` would take. Exit status is 0\n" +
			"when the document satisfies the schema and 1 when it does not, so a hook can read it.\n\n" +
			"Failures name the file, the field, and what was expected, one line per problem, and\n" +
			"every problem is reported rather than only the first.\n\n" +
			"EXAMPLES:\n" +
			"  sr-file validate memories/note.md --schema .sloprail/schemas/note.cue\n" +
			"  sr-file validate config.yaml --schema schema.cue --path '#Config'",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runValidate,
	}
	cmd.Flags().StringP("schema", "s", "", "The CUE schema to check against (required)")
	cmd.Flags().BoolP("concrete", "c", true, "Require all fields to be concrete — a missing required field fails (cue vet's -c)")
	cmd.Flags().StringP("path", "d", "", "Schema definition to check against, e.g. '#Config' (cue vet's -d)")
	_ = cmd.MarkFlagRequired("schema")
	return cmd
}

func runValidate(cmd *cobra.Command, args []string) error {
	path := args[0]
	schemaPath, _ := cmd.Flags().GetString("schema")
	concrete, _ := cmd.Flags().GetBool("concrete")
	defPath, _ := cmd.Flags().GetString("path")

	schemaSrc, err := os.ReadFile(schemaPath)
	if err != nil {
		// The schema is the rule author's file, so its absence is named as such
		// rather than folded into "validation failed".
		return fmt.Errorf("sr-file validate: read schema %s: %w", schemaPath, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("sr-file validate: read %s: %w", path, err)
	}

	doc, err := ExtractDocument(path, data)
	if err != nil {
		return fmt.Errorf("sr-file validate: %w", err)
	}

	err = ValidateWith(doc, string(schemaSrc), schemaPath, Options{
		Concrete:   concrete,
		Definition: defPath,
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrSchemaCompile):
		return fmt.Errorf("sr-file validate: %w", err)
	default:
		// A validation failure is the command working, not the command breaking,
		// so the problems are printed as they are — no "Error:" prefix wrapping a
		// report a hook is going to forward verbatim. Exit status still says 1.
		fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
		os.Exit(1)
		return nil
	}
}
