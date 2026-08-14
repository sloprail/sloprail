// This file checks a document against a CUE schema and renders what went wrong
// in a form a hook can hand to an agent.
//
// CUE does the checking. What is added here is the reporting, because CUE's own
// error positions are written for a person sitting in front of `cue vet` with
// both files open, and a hook has a different reader: an agent that knows only
// what the message says. Two gaps had to be closed, both found by running the
// binary rather than by reading the library:
//
//  1. A MISSING REQUIRED FIELD is reported by CUE at the SCHEMA's position and
//     nowhere else — `owner: field is required but not present: ./schema.cue:3:1`.
//     There is no document position to report, since the whole complaint is that
//     nothing is there. The file being validated is not named at all. So the
//     file name is supplied here, from the Document, for every problem.
//
//  2. Positions CUE reports inside a `.md`'s frontmatter are counted from the
//     start of the FRONTMATTER, not the start of the file, because the
//     frontmatter is what it was handed. Line 3 of the document is line 4 of the
//     file. Reporting the un-shifted number sends the agent to the wrong line
//     with full confidence, so LineOffset is added back.
package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
	"cuelang.org/go/encoding/json"
	"cuelang.org/go/encoding/yaml"
)

// ErrSchemaCompile marks a schema that is not valid CUE. It is separated from a
// validation failure because the two call for opposite responses: a schema that
// does not compile is the RULE AUTHOR's mistake, and telling the agent whose
// write was blocked to go fix its frontmatter would be sending it to correct a
// file that is not wrong.
var ErrSchemaCompile = errors.New("schema does not compile")

// ErrDocumentParse marks a document that is not well-formed in its own format —
// YAML that does not parse, JSON that does not parse. Distinct from a schema
// violation: the document never got as far as being checked.
var ErrDocumentParse = errors.New("document does not parse")

// Problem is one thing wrong with the document, resolved to a position in the
// FILE the caller named.
type Problem struct {
	// Path is the file. Always set — this is the field CUE does not give us for
	// a missing required field, and the one a hook's reader needs first.
	Path string

	// Field is the dotted path of the offending field, as CUE names it
	// ("status", "spec.replicas"). Empty when the problem is not attributable to
	// one field.
	Field string

	// Line and Column are 1-based positions in the FILE, already shifted past
	// any frontmatter fence. Zero when CUE reported no document position — which
	// is exactly the missing-required-field case, where there is no position in
	// the document because the field is absent from it.
	Line, Column int

	// Message is what was expected, in CUE's words: "field is required but not
	// present", `invalid value "Nikita123" (out of bound =~"^[a-z]+$")`.
	Message string
}

// String renders one problem as a hook should report it: which file, which
// field, what was expected. The position is included only when there is one —
// printing `file.md:0:0` for an absent field would be a position that does not
// exist, stated as though it did.
func (p Problem) String() string {
	var b strings.Builder
	b.WriteString(p.Path)
	if p.Line > 0 {
		fmt.Fprintf(&b, ":%d", p.Line)
		if p.Column > 0 {
			fmt.Fprintf(&b, ":%d", p.Column)
		}
	}
	b.WriteString(": ")
	if p.Field != "" {
		b.WriteString(p.Field)
		b.WriteString(": ")
	}
	b.WriteString(p.Message)
	return b.String()
}

// ValidationError is every problem found in one document, not the first.
//
// Not the first, because an agent that fixes one field, re-runs, and is refused
// again for the next is paying a round trip per mistake — and a frontmatter
// usually carries one misunderstanding expressed several times.
type ValidationError struct {
	Path     string
	Problems []Problem
}

func (e *ValidationError) Error() string {
	lines := make([]string, 0, len(e.Problems))
	for _, p := range e.Problems {
		lines = append(lines, p.String())
	}
	return strings.Join(lines, "\n")
}

// Options are the knobs `cue vet` exposes that apply here.
type Options struct {
	// Concrete requires every field to have a concrete value — `cue vet -c`.
	// This is what makes a MISSING required field a failure rather than a value
	// that is merely still open.
	Concrete bool

	// Definition selects a named definition inside the schema to check against,
	// e.g. "#Config" — `cue vet -d`. Empty checks against the schema's top level.
	Definition string
}

// Validate checks doc against the CUE schema in schemaSrc, requiring concrete
// values. Equivalent to ValidateWith with Options{Concrete: true}.
func Validate(doc Document, schemaSrc, schemaName string) error {
	return ValidateWith(doc, schemaSrc, schemaName, Options{Concrete: true})
}

// ValidateWith checks doc against the CUE schema in schemaSrc.
//
// Returns nil when the document satisfies the schema, a *ValidationError when it
// does not, and an error wrapping ErrSchemaCompile or ErrDocumentParse when the
// check could not be performed at all.
func ValidateWith(doc Document, schemaSrc, schemaName string, opts Options) error {
	ctx := cuecontext.New()

	schema := ctx.CompileString(schemaSrc, cue.Filename(schemaName))
	if err := schema.Err(); err != nil {
		return fmt.Errorf("%w: %s", ErrSchemaCompile, renderCUE(err))
	}

	if opts.Definition != "" {
		schema = schema.LookupPath(cue.ParsePath(opts.Definition))
		if err := schema.Err(); err != nil {
			return fmt.Errorf("%w: %s: %s", ErrSchemaCompile, opts.Definition, renderCUE(err))
		}
		if !schema.Exists() {
			return fmt.Errorf("%w: %s: no such definition in %s", ErrSchemaCompile, opts.Definition, schemaName)
		}
	}

	value, err := decode(ctx, doc)
	if err != nil {
		return err
	}

	unified := schema.Unify(value)

	// cue.Concrete(true) is what makes a MISSING required field a failure rather
	// than an under-specified value that is merely still open. Without it a
	// document missing every required field validates clean, which is a
	// validator that permits exactly what it was installed to refuse.
	//
	// cue.All() asks for every problem rather than stopping at the first.
	cueOpts := []cue.Option{cue.All()}
	if opts.Concrete {
		cueOpts = append(cueOpts, cue.Concrete(true))
	}
	if err := unified.Validate(cueOpts...); err != nil {
		problems := toProblems(err, doc)
		if len(problems) == 0 {
			// Defensive: an error CUE gave us that decomposed into nothing must
			// still refuse, and must still say something. Reporting "valid"
			// because we could not parse the complaint would turn a bug here
			// into permission.
			problems = []Problem{{Path: doc.Path, Message: renderCUE(err)}}
		}
		return &ValidationError{Path: doc.Path, Problems: problems}
	}
	return nil
}

// decode turns the document's bytes into a CUE value in its own format.
func decode(ctx *cue.Context, doc Document) (cue.Value, error) {
	switch doc.Format {
	case "yaml":
		file, err := yaml.Extract(doc.Path, doc.Data)
		if err != nil {
			return cue.Value{}, fmt.Errorf("%s: %w: %s", doc.Path, ErrDocumentParse, renderCUE(err))
		}
		v := ctx.BuildFile(file)
		if err := v.Err(); err != nil {
			return cue.Value{}, fmt.Errorf("%s: %w: %s", doc.Path, ErrDocumentParse, renderCUE(err))
		}
		return v, nil
	case "json":
		expr, err := json.Extract(doc.Path, doc.Data)
		if err != nil {
			return cue.Value{}, fmt.Errorf("%s: %w: %s", doc.Path, ErrDocumentParse, renderCUE(err))
		}
		v := ctx.BuildExpr(expr)
		if err := v.Err(); err != nil {
			return cue.Value{}, fmt.Errorf("%s: %w: %s", doc.Path, ErrDocumentParse, renderCUE(err))
		}
		return v, nil
	default:
		// Unreachable via ExtractDocument, which is the only producer of a
		// Document; kept so a future format added there without a case here
		// fails loudly instead of validating nothing.
		return cue.Value{}, fmt.Errorf("%s: unknown document format %q", doc.Path, doc.Format)
	}
}

// toProblems decomposes a CUE error into one Problem per complaint, each carrying
// the FILE (which CUE does not always supply) and a position in the file (which
// CUE reports relative to the document it was handed).
func toProblems(err error, doc Document) []Problem {
	var out []Problem
	seen := map[string]bool{}

	for _, e := range cueerrors.Errors(err) {
		field := strings.Join(e.Path(), ".")
		msgFormat, msgArgs := e.Msg()
		message := fmt.Sprintf(msgFormat, msgArgs...)

		// The same empty rendering renderCUE guards against, guarded here for
		// the same reason. CUE reports some faults as the format "%s" with a
		// single argument that itself renders empty, so Sprintf yields "" while
		// the error's own Error() carries the whole sentence.
		//
		// Validate's len(problems)==0 fallback cannot catch it, exactly as
		// renderCUE's len(parts)==0 could not: there IS a problem, its Message
		// is merely empty, so the guard passes and Problem.String() prints
		// "path: " and stops. A refusal an author cannot read is the failure
		// this file's reporting half exists to prevent.
		//
		// Added by the twin question rather than by a reproduction — the parse
		// path reaches this shape and the validation path was not shown to.
		// Guarding the unreachable half costs one comparison; leaving one of two
		// identical constructions unguarded is how the first one got missed.
		if strings.TrimSpace(message) == "" {
			if fallback := strings.TrimSpace(e.Error()); fallback != "" {
				message = fallback
			}
		}

		// A disjunction that fails emits a COUNTING line beside the real ones —
		// "3 errors in empty disjunction" — which carries no position and names
		// no expectation. It restates that the errors below exist. An agent
		// reading it learns nothing it does not learn from them, so it is
		// dropped rather than forwarded as though it were a fourth problem.
		if isDisjunctionSummary(message) {
			continue
		}

		p := Problem{Path: doc.Path, Field: field, Message: message}

		// Prefer a position INSIDE THE DOCUMENT. A CUE error carries several
		// positions — for a conflict, both the schema's and the document's — and
		// the one worth reporting is the one in the file the author must edit.
		// Positions in the schema are dropped: sending an agent to a line of the
		// rule that refused it is sending it to change the rule.
		if pos, ok := documentPosition(e, doc); ok {
			p.Line = pos.Line + doc.LineOffset
			p.Column = pos.Column
		}

		// The same complaint can arrive more than once — a disjunction reports
		// per branch. Deduplicate on the rendered form.
		key := p.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}

	// Stable order: by line, then field, then message. An unordered report reads
	// differently run to run for the same file, which makes a diff of two runs
	// noise instead of signal.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return out[i].Message < out[j].Message
	})
	return out
}

// isDisjunctionSummary reports whether a CUE message is the counting line a
// failed disjunction emits alongside its real branch errors — "3 errors in empty
// disjunction". Matched on the distinctive tail rather than the whole string
// because the leading count varies with the number of branches.
//
// Dropping this cannot silence a refusal on its own: Validate treats an error
// that decomposes to NO problems as still a refusal, and reports the raw CUE
// text. A document is never permitted because a message was filtered.
func isDisjunctionSummary(message string) bool {
	return strings.HasSuffix(message, "errors in empty disjunction:") ||
		strings.HasSuffix(message, "errors in empty disjunction")
}

// documentPosition finds the position among a CUE error's positions that lies in
// the DOCUMENT rather than in the schema, matching on the filename the document
// was extracted under.
func documentPosition(e cueerrors.Error, doc Document) (token.Position, bool) {
	candidates := append([]token.Pos{e.Position()}, e.InputPositions()...)
	for _, pos := range candidates {
		if !pos.IsValid() {
			continue
		}
		p := pos.Position()
		if p.Filename == doc.Path {
			return p, true
		}
	}
	return token.Position{}, false
}

// renderCUE flattens a CUE error to a single line. Details() is multi-line and
// carries positions we have already resolved ourselves where it mattered.
func renderCUE(err error) string {
	var parts []string
	for _, e := range cueerrors.Errors(err) {
		format, args := e.Msg()
		msg := fmt.Sprintf(format, args...)
		if strings.TrimSpace(msg) == "" {
			// Msg() rendered to nothing, and the complaint is not nothing.
			//
			// CUE's scanner reports some faults — "control characters are not
			// allowed", which is what any binary input hits — as the format "%s"
			// with a single argument that itself renders empty. Sprintf then
			// yields "", so this loop produced a part that says nothing while
			// the error's OWN Error() carries the whole sentence.
			//
			// The len(parts)==0 fallback below cannot catch it: there IS a part,
			// it is merely empty, so the guard passes and the caller formats
			// "%w: %s" onto nothing. Measured through the shipped binary before
			// this: `head -c 64 /dev/urandom | sr-file validate - --as .yaml`
			// printed "-: document does not parse: " with the reason missing —
			// exit 1, correctly refusing, and no word about why.
			//
			// A refusal an author cannot read is the failure this file's whole
			// reporting half exists to prevent, so the sub-error's own rendering
			// stands in. It already carries its position, which is why the
			// position is not prepended again below.
			if fallback := strings.TrimSpace(e.Error()); fallback != "" {
				parts = append(parts, fallback)
				continue
			}
		}
		if pos := e.Position(); pos.IsValid() {
			msg = fmt.Sprintf("%s: %s", pos.Position(), msg)
		}
		parts = append(parts, msg)
	}
	if len(parts) == 0 {
		return err.Error()
	}
	return strings.Join(parts, "; ")
}
