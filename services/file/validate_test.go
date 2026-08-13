package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noteSchema carries several constraint kinds at once: a required field, an enum
// (disjunction), a regex bound, and a numeric range. Each failure mode below is
// checked against it so the tests exercise one schema an author would plausibly
// write rather than one contrived per assertion.
const noteSchema = `
title!:    string & !=""
status!:   "draft" | "review" | "published"
owner!:    =~"^[a-z]+$"
priority?: int & >=1 & <=5
`

func mustDoc(t *testing.T, path, src string) Document {
	t.Helper()
	doc, err := ExtractDocument(path, []byte(src))
	require.NoError(t, err)
	return doc
}

func TestValidate_AcceptsAConformingDocument(t *testing.T) {
	doc := mustDoc(t, "note.md", "---\ntitle: A note\nstatus: draft\nowner: nikita\npriority: 2\n---\n\nProse.\n")
	assert.NoError(t, Validate(doc, noteSchema, "schema.cue"))
}

// The whole point of the command. A hook runs it to learn WHAT is wrong, so the
// message must carry the file, the field, and the expectation — not merely be
// non-empty.
func TestValidate_MissingRequiredFieldNamesFileFieldAndExpectation(t *testing.T) {
	doc := mustDoc(t, "note.md", "---\ntitle: A note\nstatus: draft\n---\n\nProse.\n")

	err := Validate(doc, noteSchema, "schema.cue")
	require.Error(t, err)

	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	require.Len(t, verr.Problems, 1)

	p := verr.Problems[0]
	assert.Equal(t, "note.md", p.Path, "which file")
	assert.Equal(t, "owner", p.Field, "which field")
	assert.Contains(t, p.Message, "required", "what was expected")

	rendered := err.Error()
	assert.Contains(t, rendered, "note.md")
	assert.Contains(t, rendered, "owner")
	assert.Contains(t, rendered, "required")
}

// CUE reports a missing required field at the SCHEMA's position and names no
// document at all. If the file name were not supplied here, a hook's message
// would point the agent at the rule instead of at its own file.
func TestValidate_MissingFieldStillNamesTheDocumentNotTheSchema(t *testing.T) {
	doc := mustDoc(t, "note.md", "---\ntitle: A note\nstatus: draft\n---\nbody\n")

	err := Validate(doc, noteSchema, "schema.cue")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "note.md")
	assert.NotContains(t, err.Error(), "schema.cue",
		"a position inside the rule sends the agent to change the rule that refused it")
}

// The offset claim, falsified directly: the bad field sits on file line 6, and
// on line 5 of the frontmatter CUE was handed. Reporting 5 would be a wrong line
// stated with the same confidence as a right one.
func TestValidate_PositionIsTheFileLineNotTheFrontmatterLine(t *testing.T) {
	src := "---\n" + // file line 1
		"title: A note\n" + // 2
		"owner: nikita\n" + // 3
		"priority: 3\n" + // 4
		"tags: []\n" + // 5
		"status: nonsense\n" + // 6  <- the offender
		"---\n\nProse.\n"
	doc := mustDoc(t, "note.md", src)

	err := Validate(doc, noteSchema, "schema.cue")
	require.Error(t, err)

	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	require.NotEmpty(t, verr.Problems)
	for _, p := range verr.Problems {
		assert.Equal(t, 6, p.Line, "status is on line 6 of the file, line 5 of the frontmatter")
	}
}

func TestValidate_ReportsEveryMissingFieldNotOnlyTheFirst(t *testing.T) {
	doc := mustDoc(t, "note.md", "---\ntitle: A note\n---\nbody\n")

	err := Validate(doc, noteSchema, "schema.cue")
	require.Error(t, err)

	var verr *ValidationError
	require.ErrorAs(t, err, &verr)

	fields := map[string]bool{}
	for _, p := range verr.Problems {
		fields[p.Field] = true
	}
	assert.True(t, fields["status"], "status is missing and must be reported")
	assert.True(t, fields["owner"], "owner is missing and must be reported")
}

func TestValidate_ReportsEveryConstraintViolationNotOnlyTheFirst(t *testing.T) {
	doc := mustDoc(t, "note.yaml", "title: A note\nstatus: draft\nowner: BADCASE\npriority: 99\n")

	err := Validate(doc, noteSchema, "schema.cue")
	require.Error(t, err)

	var verr *ValidationError
	require.ErrorAs(t, err, &verr)

	fields := map[string]bool{}
	for _, p := range verr.Problems {
		fields[p.Field] = true
	}
	assert.True(t, fields["owner"], "the regex bound is violated")
	assert.True(t, fields["priority"], "the range bound is violated")
}

// A failed disjunction emits a counting line beside the real branch errors. It
// is dropped, and dropping it must not cost the expectation itself.
func TestValidate_EnumFailureCarriesTheAllowedValuesAndNoCountingLine(t *testing.T) {
	doc := mustDoc(t, "note.md", "---\ntitle: A note\nstatus: nonsense\nowner: nikita\n---\nbody\n")

	err := Validate(doc, noteSchema, "schema.cue")
	require.Error(t, err)

	rendered := err.Error()
	assert.NotContains(t, rendered, "empty disjunction",
		"the counting line restates that the errors below exist and names no expectation")
	assert.Contains(t, rendered, "note.md")
	assert.Contains(t, rendered, "status")
	// The allowed values are what the agent needs in order to fix the file.
	assert.Contains(t, rendered, "draft")
	assert.Contains(t, rendered, "review")
	assert.Contains(t, rendered, "published")
}

func TestValidate_RegexViolationSaysWhatWasExpected(t *testing.T) {
	doc := mustDoc(t, "note.yaml", "title: A note\nstatus: draft\nowner: Nikita123\n")

	err := Validate(doc, noteSchema, "schema.cue")
	require.Error(t, err)
	rendered := err.Error()
	assert.Contains(t, rendered, "owner")
	assert.Contains(t, rendered, "^[a-z]+$", "the pattern is the expectation")
	assert.Contains(t, rendered, "note.yaml")
}

func TestValidate_TypeMismatchSaysBothTypes(t *testing.T) {
	doc := mustDoc(t, "note.yaml", "title: A note\nstatus: draft\nowner: nikita\npriority: \"high\"\n")

	err := Validate(doc, noteSchema, "schema.cue")
	require.Error(t, err)
	rendered := err.Error()
	assert.Contains(t, rendered, "priority")
	assert.Contains(t, rendered, "int")
}

func TestValidate_SchemaThatDoesNotCompileIsItsOwnClassOfFault(t *testing.T) {
	doc := mustDoc(t, "note.yaml", "title: A note\n")

	err := Validate(doc, "title!: string &", "schema.cue")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSchemaCompile,
		"a schema that does not compile is the rule author's mistake, not the agent's")

	var verr *ValidationError
	assert.NotErrorAs(t, err, &verr, "it is not a statement about the document")
}

func TestValidate_DocumentThatDoesNotParseIsItsOwnClassOfFault(t *testing.T) {
	doc := mustDoc(t, "note.yaml", "title: [unclosed\n")

	err := Validate(doc, noteSchema, "schema.cue")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDocumentParse)
	assert.Contains(t, err.Error(), "note.yaml")
}

func TestValidate_JSONDocument(t *testing.T) {
	ok := mustDoc(t, "note.json", `{"title":"A note","status":"draft","owner":"nikita"}`)
	assert.NoError(t, Validate(ok, noteSchema, "schema.cue"))

	bad := mustDoc(t, "note.json", `{"title":"A note","status":"draft"}`)
	err := Validate(bad, noteSchema, "schema.cue")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "note.json")
	assert.Contains(t, err.Error(), "owner")
}

// Without Concrete, a missing required field is an open value rather than a
// failure — which would be a validator permitting exactly what it was installed
// to refuse. The default is on; this pins that the flag is what does it.
func TestValidateWith_ConcreteIsWhatMakesAMissingFieldFail(t *testing.T) {
	doc := mustDoc(t, "note.yaml", "title: A note\nstatus: draft\n")

	require.Error(t, ValidateWith(doc, noteSchema, "schema.cue", Options{Concrete: true}))
	assert.NoError(t, ValidateWith(doc, noteSchema, "schema.cue", Options{Concrete: false}),
		"without -c an absent required field is merely not yet supplied")
}

func TestValidateWith_DefinitionSelectsASchemaInsideTheFile(t *testing.T) {
	const schema = `
#Note: {
	title!:  string
	status!: "draft" | "published"
}
#Other: {
	unrelated!: int
}
`
	doc := mustDoc(t, "note.yaml", "title: A note\nstatus: draft\n")

	assert.NoError(t, ValidateWith(doc, schema, "schema.cue", Options{Concrete: true, Definition: "#Note"}))

	err := ValidateWith(doc, schema, "schema.cue", Options{Concrete: true, Definition: "#Other"})
	assert.Error(t, err, "the document does not satisfy #Other")
}

func TestValidateWith_UnknownDefinitionIsASchemaFault(t *testing.T) {
	doc := mustDoc(t, "note.yaml", "title: A note\n")

	err := ValidateWith(doc, "#Note: {title!: string}", "schema.cue", Options{Definition: "#Nope"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSchemaCompile)
}

// The way a validator most commonly fails is by permitting a document that has
// nothing in it — an empty frontmatter is not "no fields to disagree with", it is
// every required field missing.
func TestValidate_EmptyDocumentIsRefusedNotPermitted(t *testing.T) {
	for name, src := range map[string]struct{ path, body string }{
		"empty frontmatter": {"note.md", "---\n---\n\nAll prose.\n"},
		"empty yaml file":   {"note.yaml", ""},
	} {
		t.Run(name, func(t *testing.T) {
			doc := mustDoc(t, src.path, src.body)
			err := Validate(doc, noteSchema, "schema.cue")
			require.Error(t, err, "an empty document is every required field missing")

			var verr *ValidationError
			require.ErrorAs(t, err, &verr)
			assert.Len(t, verr.Problems, 3, "title, status and owner are all absent")
		})
	}
}

// A frontmatter that is a list where the schema wants a struct must be refused
// rather than skipped as something the schema has no opinion about.
func TestValidate_DocumentOfTheWrongShapeIsRefused(t *testing.T) {
	doc := mustDoc(t, "note.md", "---\n- a\n- b\n---\nbody\n")

	err := Validate(doc, noteSchema, "schema.cue")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "note.md")
}

// A disjunction whose branches all fail can produce an error carrying only the
// summary line this code filters. Filtering it must not turn a refusal into a
// permit, and must not leave the refusal with nothing to say.
func TestValidate_FilteringTheSummaryNeverSilencesARefusal(t *testing.T) {
	const schema = `x!: {a!: int} | {b!: string}`
	doc := mustDoc(t, "note.yaml", "x:\n  c: 1\n")

	err := Validate(doc, schema, "schema.cue")
	require.Error(t, err, "the document satisfies neither branch")
	assert.NotEmpty(t, strings.TrimSpace(err.Error()), "a refusal with no reason leaves the agent guessing")
	assert.Contains(t, err.Error(), "note.yaml")
}

// CUE's own behaviour, pinned so a version bump that changes it is visible: when
// a PRESENT field violates a constraint, CUE stops before reporting fields that
// are ABSENT. So a document with both kinds of fault reports only the first kind,
// and an agent fixing the reported value is refused again for the missing field.
//
// This is CUE's evaluation order, not something this code chooses, and there is
// no flag that turns it off — `--all-errors` does not change it either.
func TestValidate_AValueErrorSuppressesMissingFieldErrors_CUELimitation(t *testing.T) {
	// priority is out of range; status and owner are both absent.
	doc := mustDoc(t, "note.yaml", "title: A note\npriority: 99\n")

	err := Validate(doc, noteSchema, "schema.cue")
	require.Error(t, err)

	var verr *ValidationError
	require.ErrorAs(t, err, &verr)

	fields := map[string]bool{}
	for _, p := range verr.Problems {
		fields[p.Field] = true
	}
	assert.True(t, fields["priority"], "the value error is reported")
	assert.False(t, fields["status"],
		"documents CUE's order: an absent field is not reported while a present one is invalid")
}

// A surprising default worth pinning: a CUE schema is OPEN, so a field the
// schema never mentions is permitted. An author who expects "these fields and no
// others" gets something else unless they close it. This matches `cue vet`, and
// is left alone deliberately — a schema written for this must mean the same
// thing handed to `cue vet`.
func TestValidate_SchemaIsOpenUnlessClosed(t *testing.T) {
	doc := mustDoc(t, "note.yaml", "title: A note\nstatus: draft\nowner: nikita\nunexpected: 1\n")

	assert.NoError(t, Validate(doc, noteSchema, "schema.cue"),
		"an open schema permits a field it never mentions")

	const closed = `close({
	title!:  string
	status!: "draft" | "review" | "published"
	owner!:  string
})`
	err := Validate(doc, closed, "schema.cue")
	require.Error(t, err, "close() is how an author refuses unknown keys")
	assert.Contains(t, err.Error(), "unexpected")
	assert.Contains(t, err.Error(), "note.yaml")
}

func TestProblem_StringOmitsAPositionItDoesNotHave(t *testing.T) {
	// A missing field has no position in the document — there is nothing there
	// to point at. Printing note.md:0:0 would state a position that does not
	// exist.
	p := Problem{Path: "note.md", Field: "owner", Message: "field is required but not present"}
	assert.Equal(t, "note.md: owner: field is required but not present", p.String())

	withPos := Problem{Path: "note.md", Field: "status", Line: 6, Column: 9, Message: "bad"}
	assert.Equal(t, "note.md:6:9: status: bad", withPos.String())
}

func TestValidationError_RendersOneProblemPerLine(t *testing.T) {
	doc := mustDoc(t, "note.md", "---\ntitle: A note\n---\nbody\n")

	err := Validate(doc, noteSchema, "schema.cue")
	require.Error(t, err)

	lines := strings.Split(err.Error(), "\n")
	assert.Len(t, lines, 2, "status and owner are both missing")
	for _, l := range lines {
		assert.Contains(t, l, "note.md", "every line stands alone for a hook forwarding it")
	}
}
