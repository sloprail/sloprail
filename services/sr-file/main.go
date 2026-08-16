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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		// A refusal has already printed its problems in the form a hook forwards
		// verbatim; anything else is a fault that has not been reported yet.
		if !errors.Is(err, errRefused) {
			fmt.Fprintln(os.Stderr, err)
		}
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
	cmd.AddCommand(newChangesCmd())
	cmd.AddCommand(newChecksCmd())
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
			"BYTES ON STDIN, with '-' as the path, for a hook holding content that is not on disk:\n" +
			"at a Pre event the write has not happened, so the pending bytes are in the event and\n" +
			"the file either does not exist or still holds the old ones. --as says how to read them,\n" +
			"and is required, because a pipe carries no name to take the format from.\n\n" +
			"--emit prints the validated document as JSON on success, so a caller can take a field\n" +
			"out of it with jq instead of parsing the same bytes a second time. Nothing is printed\n" +
			"for a document that failed.\n\n" +
			"EXAMPLES:\n" +
			"  sr-file validate memories/note.md --schema .sloprail/schemas/note.cue\n" +
			"  sr-file validate config.yaml --schema schema.cue --path '#Config'\n" +
			"  jq -r .event.fields.content event.json | sr-file validate - --as .md --schema s.cue\n" +
			"  sr-file validate DECISION.md --schema s.cue --emit | jq -r .transcript_path",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runValidate,
	}
	cmd.Flags().StringP("schema", "s", "", "The CUE schema to check against (required)")
	cmd.Flags().BoolP("concrete", "c", true, "Require all fields to be concrete — a missing required field fails (cue vet's -c)")
	cmd.Flags().StringP("path", "d", "", "Schema definition to check against, e.g. '#Config' (cue vet's -d)")
	cmd.Flags().String("as", "", "How to read bytes on stdin: .md, .yaml or .json. Required with '-', and refused with a path")
	cmd.Flags().Bool("emit", false, "On success, print the validated document as JSON on stdout")
	_ = cmd.MarkFlagRequired("schema")
	return cmd
}

// stdinArg is the path that means "the bytes are on standard input", spelled the
// way every other command spells it.
const stdinArg = "-"

// readInput is the bytes to check and the name to report them under.
//
// Two routes that differ in where the bytes come from and in nothing else. A
// PATH reads the file and takes the format from its extension. STDIN reads the
// pipe, and the format has to be stated, because bytes arriving through a pipe
// carry no name to read one off.
//
// A hook holding PENDING content is why the second route exists: at a Pre event
// the write has not happened, so the bytes that matter are in the event and the
// path on disk either does not exist or still holds the old ones. Without this
// such a hook has to materialise a tempfile whose name ends in the right
// extension — and on macOS `mktemp -t x.XXXXXX.md` appends its randomness AFTER
// the template, so the extension becomes the random suffix and every file is
// refused on its name, which looks exactly like the schema refusing it.
func readInput(cmd *cobra.Command, path, as string) (Document, error) {
	if path != stdinArg {
		// A named file already says what it is; taking --as here as well would be
		// two answers to one question, with the flag silently winning over the
		// name the caller can see.
		if as != "" {
			return Document{}, fmt.Errorf("sr-file validate: --as applies to bytes read from stdin ('-'), but a path was given: %s already says what it is", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return Document{}, fmt.Errorf("sr-file validate: read %s: %w", path, err)
		}
		doc, err := ExtractDocument(path, data)
		if err != nil {
			return Document{}, fmt.Errorf("sr-file validate: %w", err)
		}
		return doc, nil
	}

	// Refused rather than defaulted to .md. Which bytes are the document is
	// decided by the format, and guessing one for content whose shape is unknown
	// produces a complaint about the wrong bytes — the failure document.go
	// exists to prevent. A default would make that the common case.
	if as == "" {
		return Document{}, fmt.Errorf("sr-file validate: reading from stdin needs --as to say how (.md, .yaml or .json) — which bytes are the document is decided by the format, and there is no file name here to read one from")
	}
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return Document{}, fmt.Errorf("sr-file validate: read stdin: %w", err)
	}
	// Named for the reader, since every message carries a file and "-" is what
	// the caller asked to be called.
	doc, err := ExtractDocumentAs(stdinArg, as, data)
	if err != nil {
		return Document{}, fmt.Errorf("sr-file validate: %w", err)
	}
	return doc, nil
}

// emitDocument prints the validated document as one JSON value.
//
// The command has already parsed these bytes in order to check them, so a
// caller wanting a field out of them — the transcript path, to test that it
// resolves — would otherwise parse the same document a second time, in a second
// language, with a second idea of where the frontmatter ends. That second
// parser is the thing this exists to delete.
//
// Only ever on SUCCESS. Emitting a document that failed would let
// `validate --emit | jq -r .field` read a value out of a file the command just
// refused, with the exit status the only thing saying otherwise — and a shell
// pipeline is exactly where an exit status goes missing.
func emitDocument(cmd *cobra.Command, doc Document) error {
	var value any
	if err := yaml.Unmarshal(doc.Data, &value); err != nil {
		return fmt.Errorf("sr-file validate: --emit: %s: %w", doc.Path, err)
	}
	out, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("sr-file validate: --emit: %s: %w", doc.Path, err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(out))
	return nil
}

func runValidate(cmd *cobra.Command, args []string) error {
	path := args[0]
	schemaPath, _ := cmd.Flags().GetString("schema")
	concrete, _ := cmd.Flags().GetBool("concrete")
	defPath, _ := cmd.Flags().GetString("path")
	as, _ := cmd.Flags().GetString("as")
	emit, _ := cmd.Flags().GetBool("emit")

	schemaSrc, err := os.ReadFile(schemaPath)
	if err != nil {
		// The schema is the rule author's file, so its absence is named as such
		// rather than folded into "validation failed".
		return fmt.Errorf("sr-file validate: read schema %s: %w", schemaPath, err)
	}

	doc, err := readInput(cmd, path, as)
	if err != nil {
		return err
	}

	err = ValidateWith(doc, string(schemaSrc), schemaPath, Options{
		Concrete:   concrete,
		Definition: defPath,
	})
	switch {
	case err == nil:
		if emit {
			return emitDocument(cmd, doc)
		}
		return nil
	case errors.Is(err, ErrSchemaCompile):
		return fmt.Errorf("sr-file validate: %w", err)
	default:
		// A validation failure is the command working, not the command breaking,
		// so the problems are printed as they are — no "Error:" prefix wrapping a
		// report a hook is going to forward verbatim. Exit status still says 1.
		//
		// Returned rather than os.Exit'd. The status is identical either way —
		// main prints nothing extra for this sentinel and exits 1 — but exiting
		// from inside RunE takes the process down mid-test, so the failing half
		// of this command's contract could not be exercised at all. A command
		// whose refusal path is untestable is the half that matters here.
		fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
		return errRefused
	}
}

// errRefused marks "the document did not satisfy the schema", already reported.
//
// It carries no message of its own: the problems went to stderr in the form a
// hook forwards verbatim, and main must not print a second line after them.
var errRefused = errors.New("")
