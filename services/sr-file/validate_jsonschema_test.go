package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A schema named *.json is a JSON Schema: the same command, the same refusals,
// for an author who has the shape as JSON Schema already (a judge's
// response_schema is one).

const invariantsSchema = `{
  "type": "object",
  "required": ["invariants"],
  "additionalProperties": false,
  "properties": {
    "invariants": {
      "type": "object",
      "additionalProperties": {
        "type": "object",
        "required": ["code", "tests", "why"],
        "additionalProperties": false,
        "properties": {
          "code": {"enum": ["pass", "fail"]},
          "tests": {"enum": ["pass", "fail"]},
          "why": {"type": "string"}
        }
      }
    }
  }
}`

func jsonSchemaOpts() Options { return Options{Concrete: true, JSONSchema: true} }

func TestIsJSONSchemaPath(t *testing.T) {
	assert.True(t, IsJSONSchemaPath("rules/response.schema.json"))
	assert.True(t, IsJSONSchemaPath("S.JSON"))
	assert.False(t, IsJSONSchemaPath("schema.cue"))
}

func TestValidateJSONSchema_AcceptsAConformingDocument(t *testing.T) {
	doc := mustDoc(t, "answer.json", `{"invariants": {"a/b": {"code": "pass", "tests": "fail", "why": "no test"}}}`)
	assert.NoError(t, ValidateWith(doc, invariantsSchema, "response.schema.json", jsonSchemaOpts()))
}

func TestValidateJSONSchema_MissingRequiredFieldIsNamed(t *testing.T) {
	doc := mustDoc(t, "answer.json", `{"invariants": {"a/b": {"code": "pass", "tests": "fail"}}}`)
	err := ValidateWith(doc, invariantsSchema, "response.schema.json", jsonSchemaOpts())
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Contains(t, err.Error(), "why")
	assert.Contains(t, err.Error(), "answer.json")
}

func TestValidateJSONSchema_EnumAndTypeViolationsAreRefused(t *testing.T) {
	for name, src := range map[string]string{
		"enum":       `{"invariants": {"a": {"code": "maybe", "tests": "pass", "why": "x"}}}`,
		"type":       `{"invariants": {"a": {"code": "pass", "tests": "pass", "why": 3}}}`,
		"additional": `{"invariants": {}, "extra": 1}`,
		"top level":  `{"pass": true}`,
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateWith(mustDoc(t, "answer.json", src), invariantsSchema, "response.schema.json", jsonSchemaOpts())
			var verr *ValidationError
			require.ErrorAs(t, err, &verr, "a document that breaks the schema is a validation error")
		})
	}
}

func TestValidateJSONSchema_ASchemaThatCannotBeReadIsItsOwnClassOfFault(t *testing.T) {
	doc := mustDoc(t, "answer.json", `{"pass": true}`)
	for name, schema := range map[string]string{
		"not json":     `{"type": `,
		"not a schema": `{"type": 7}`,
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateWith(doc, schema, "response.schema.json", jsonSchemaOpts())
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrSchemaCompile, "the schema is the rule author's file, not the document")
		})
	}
}
