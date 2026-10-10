package declaration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A judge check's two optional answer-shaping fields, at load: `response_schema` (the
// shape the judge answers in) and `post_process` (a script run on that answer).

const (
	verdictSchema = `{"type": "object", "required": ["pass", "reasoning"],
  "properties": {"pass": {"type": "boolean"}, "reasoning": {"type": "string"}, "severity": {"enum": ["low", "high"]}}}`
	tableOnlySchema = `{"type": "object", "required": ["invariants"], "properties": {"invariants": {"type": "object"}}}`
	postProcessSh   = "#!/bin/sh\ncat\n"
)

func shapedGuard(checkLines string) string {
	return "\nmatch: \"**/*.md\"\nchecks:\n  - judge: ./j.md.j2\n" + checkLines
}

// Both fields load on a judge and are kept on the parsed check; a rule that sets
// neither is unchanged.
func TestLoad_Check_ResponseSchemaAndPostProcessOnAJudge(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/table/file-guard.yaml":       shapedGuard("    response_schema: ./response.schema.json\n    post_process: ./post-process.sh\n"),
		"file-guard/table/response.schema.json":  tableOnlySchema,
		"file-guard/table/post-process.sh":       postProcessSh,
		"file-guard/plain/file-guard.yaml":       shapedGuard(""),
		"file-guard/schema/file-guard.yaml":      shapedGuard("    response_schema: ./response.schema.json\n"),
		"file-guard/schema/response.schema.json": verdictSchema,
		"file-guard/post/file-guard.yaml":        shapedGuard("    post_process: ./post-process.sh\n"),
		"file-guard/post/post-process.sh":        postProcessSh,
	})
	require.Len(t, loaded.FileGuards, 4)
	byName := map[string]Check{}
	for _, g := range loaded.FileGuards {
		byName[g.Name] = g.Checks[0]
	}
	assert.Equal(t, "./response.schema.json", byName["table"].ResponseSchema)
	assert.Equal(t, "./post-process.sh", byName["table"].PostProcess)
	assert.Empty(t, byName["plain"].ResponseSchema)
	assert.Empty(t, byName["plain"].PostProcess)
	assert.Equal(t, "./response.schema.json", byName["schema"].ResponseSchema, "a schema that still carries the verdict needs no post_process")
	assert.Equal(t, "./post-process.sh", byName["post"].PostProcess, "a post_process needs no schema")
}

// A response_schema whose answer carries no verdict, on a check with no post_process
// to work one out, does not load, and the reason says a post_process is needed.
// sr:proves judges/response-schema-without-post-process-carries-the-verdict
func TestLoad_Check_ResponseSchemaWithoutVerdictNeedsPostProcess(t *testing.T) {
	for name, schema := range map[string]string{
		"no pass at all":         tableOnlySchema,
		"pass is not a boolean":  `{"type": "object", "required": ["pass"], "properties": {"pass": {"type": "string"}, "reasoning": {"type": "string"}}}`,
		"reasoning not a string": `{"type": "object", "required": ["pass"], "properties": {"pass": {"type": "boolean"}, "reasoning": {"type": "array"}}}`,
		"no reasoning":           `{"type": "object", "required": ["pass"], "properties": {"pass": {"type": "boolean"}}}`,
		"pass is optional":       `{"type": "object", "properties": {"pass": {"type": "boolean"}, "reasoning": {"type": "string"}}}`,
		"pass only nested":       `{"type": "object", "properties": {"verdict": {"type": "object", "required": ["pass"], "properties": {"pass": {"type": "boolean"}, "reasoning": {"type": "string"}}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			iv := loadOneInvalid(t, map[string]string{
				"file-guard/table/file-guard.yaml":      shapedGuard("    response_schema: ./response.schema.json\n"),
				"file-guard/table/response.schema.json": schema,
			})
			assert.True(t, hasKind(iv, ErrSchemaNeedsPostProcess), "%v", iv.Reason)
			assert.Contains(t, iv.Reason, "post_process is needed")
		})
	}
	// The same schema loads once a post_process is there to read the verdict out of it.
	loaded := loadOK(t, map[string]string{
		"file-guard/table/file-guard.yaml":      shapedGuard("    response_schema: ./response.schema.json\n    post_process: ./post-process.sh\n"),
		"file-guard/table/response.schema.json": tableOnlySchema,
		"file-guard/table/post-process.sh":      postProcessSh,
	})
	require.Len(t, loaded.FileGuards, 1)
}

// A response_schema that is not there, is not a JSON object, or describes something
// other than an object is refused at load.
func TestLoad_Check_BadResponseSchema(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"missing":       {},
		"not json":      {"file-guard/table/response.schema.json": "type: object\n"},
		"a json array":  {"file-guard/table/response.schema.json": `["pass"]`},
		"not an object": {"file-guard/table/response.schema.json": `{"type": "array"}`},
	} {
		t.Run(name, func(t *testing.T) {
			files["file-guard/table/file-guard.yaml"] = shapedGuard("    response_schema: ./response.schema.json\n    post_process: ./post-process.sh\n")
			files["file-guard/table/post-process.sh"] = postProcessSh
			iv := loadOneInvalid(t, files)
			assert.True(t, hasKind(iv, ErrBadResponseSchema), "%v", iv.Reason)
		})
	}
}

// Both fields shape a judge's answer; on a script check they are a load error.
func TestLoad_Check_StrayResponseShapingOnScript(t *testing.T) {
	for name, line := range map[string]string{
		"response_schema": "    response_schema: ./response.schema.json\n",
		"post_process":    "    post_process: ./post-process.sh\n",
	} {
		t.Run(name, func(t *testing.T) {
			iv := loadOneInvalid(t, map[string]string{
				"file-guard/stray/file-guard.yaml": "\nmatch: \"**/*.md\"\nchecks:\n  - script: ./s.sh\n" + line,
			})
			assert.True(t, hasKind(iv, ErrStrayResponseShaping), "%v", iv.Reason)
		})
	}
}

// A post_process that cannot be exec'd is the same environment fault as a prepare that
// cannot: the rule stays loaded and enforced, and is reported.
func TestLoad_Check_PostProcessThatCannotRunKeepsTheRuleLoaded(t *testing.T) {
	dir := writeDecl(t, map[string]string{
		"file-guard/post/file-guard.yaml": shapedGuard("    post_process: ./post-process.sh\n"),
		"file-guard/post/post-process.sh": postProcessSh,
	})
	require.NoError(t, os.Chmod(filepath.Join(dir, "file-guard/post/post-process.sh"), 0o644))
	loaded, err := New(dir).Load(testRegistry(t))
	require.NoError(t, err)
	require.Len(t, loaded.FileGuards, 1, "the rule stays loaded")
	require.NotEmpty(t, loaded.Degraded, "and is reported")
	assert.Contains(t, loaded.Degraded[0].Reason, "post_process")
}
