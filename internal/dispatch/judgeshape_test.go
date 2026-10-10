package dispatch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A judge check's response_schema and post_process, through the real verify script:
// run by a stand-in sr-agent that re-asks as the real one does, and by a mock.

var (
	srFileOnce sync.Once
	srFileDir  string
	srFileErr  error
)

// withSrFile puts a freshly built sr-file first on PATH: the verify script runs it by
// name to check an answer against its schema.
func withSrFile(t *testing.T) {
	t.Helper()
	srFileOnce.Do(func() {
		srFileDir, srFileErr = os.MkdirTemp("", "dispatch-sr-file-")
		if srFileErr != nil {
			return
		}
		cmd := exec.Command("go", "build", "-o", filepath.Join(srFileDir, "sr-file"), "github.com/sloprail/sloprail/services/sr-file")
		if out, err := cmd.CombinedOutput(); err != nil {
			srFileErr = &buildError{string(out), err}
		}
	})
	require.NoError(t, srFileErr, "sr-file must build: the verify script validates answers with it")
	t.Setenv("PATH", srFileDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type buildError struct {
	out string
	err error
}

func (e *buildError) Error() string { return e.err.Error() + ": " + e.out }

const tableSchema = `{
  "type": "object",
  "required": ["invariants"],
  "additionalProperties": false,
  "properties": {
    "invariants": {
      "type": "object",
      "additionalProperties": {
        "type": "object",
        "required": ["code", "why"],
        "additionalProperties": false,
        "properties": {"code": {"enum": ["pass", "fail"]}, "why": {"type": "string"}}
      }
    }
  }
}`

// tablePostProcess is the motivating post_process: every id it was handed must be
// answered (else the answer is unusable), pass is "all pass", reasoning names the
// failing ones, and the table is the metadata.
const tablePostProcess = `#!/bin/sh
answer="$(cat)"
want="$(jq -r '.additionalContext.ids[]' "$SR_JUDGE_INPUT")"
for id in $want; do
  printf '%s' "$answer" | jq -e --arg id "$id" '.invariants[$id]' >/dev/null || {
    echo "the answer has no entry for invariant $id" >&2
    exit 65
  }
done
printf '%s' "$answer" | jq -c '{
  pass: ([.invariants[] | .code] | all(. == "pass")),
  reasoning: ([.invariants | to_entries[] | select(.value.code == "fail") | "\(.key): \(.value.why)"] | join("; ")),
  metadata: {invariants: .invariants, guard: env.SR_GUARDRAIL, dir: env.SR_GUARDRAIL_DIR, tree: env.SR_TREE}
}'
`

// shapedRule is a rule folder with the rubric, the schema and a post_process, and the
// judgeCall that names them.
func shapedRule(t *testing.T, schema, postProcess string) judgeCall {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is not on PATH: the judge's verify script needs it (install jq)")
	}
	dir := t.TempDir()
	j := judgeCall{
		Dir: dir, Template: "./rubric.md.j2", GuardName: "table", Qualified: "file-guard/table",
		InputJSON: []byte(`{"additionalContext": {"ids": ["a/one", "a/two"]}}`),
		Env:       []string{"SR_TREE=/the/tree"},
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rubric.md.j2"), []byte("rubric"), 0o644))
	if schema != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "response.schema.json"), []byte(schema), 0o644))
		j.ResponseSchema = "./response.schema.json"
	}
	if postProcess != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "post-process.sh"), []byte(postProcess), 0o755))
		j.PostProcess = "./post-process.sh"
	}
	return j
}

// agentAnswering installs a stand-in sr-agent that gives the judge the listed answers,
// one per attempt, and runs the verify script on each as the real one does: exit 0
// accepts, exit 3 is final, any other exit asks again with the next answer. It records
// the prompt it was given and how many attempts it made.
func agentAnswering(t *testing.T, answers ...string) (bin string) {
	t.Helper()
	bin = t.TempDir()
	for i, a := range answers {
		require.NoError(t, os.WriteFile(filepath.Join(bin, "answer."+string(rune('1'+i))), []byte(a), 0o644))
	}
	installStubOnPath(t, bin, "sr-agent", `#!/bin/sh
here="$(dirname "$0")"
verify=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--verify" ]; then verify="$2"; shift; fi
  shift
done
cat > "$here/prompt"
n=0
for a in "$here"/answer.*; do
  n=$((n + 1))
  echo "$n" > "$here/attempts"
  out="$("$verify" "$a" < "$a" 2>&1)"
  code=$?
  [ -n "$out" ] && echo "sr-agent: verifier (attempt $n/2): $out" >&2
  [ "$code" -eq 0 ] && exit 0
  [ "$code" -eq 3 ] && exit 1
  cp "$here/prompt" "$here/complaint.$n"
  printf '%s' "$out" > "$here/complaint.$n"
done
exit 1
`)
	return bin
}

func attempts(t *testing.T, bin string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(bin, "attempts"))
	require.NoError(t, err)
	return strings.TrimSpace(string(b))
}

const (
	tablePass = `{"invariants": {"a/one": {"code": "pass", "why": "held"}, "a/two": {"code": "pass", "why": "held"}}}`
	tableFail = `{"invariants": {"a/one": {"code": "pass", "why": "held"}, "a/two": {"code": "fail", "why": "no test proves it"}}}`
)

// With a post_process the verdict is the one it works out of the answer, a pass and a
// fail alike, and what it returns as metadata rides on the verdict.
// sr:proves judges/post-process-decides-the-verdict
func TestShapedJudge_PostProcessDecidesTheVerdictAndReturnsMetadata(t *testing.T) {
	withSrFile(t)
	t.Run("pass", func(t *testing.T) {
		j := shapedRule(t, tableSchema, tablePostProcess)
		agentAnswering(t, tablePass)
		v, err := askJudge(j, "the rubric")
		require.NoError(t, err)
		require.False(t, v.Refused, "every invariant passed: %s", v.Reason)
		table, _ := v.Metadata["invariants"].(map[string]any)
		require.Len(t, table, 2, "the metadata holds the per-invariant table: %v", v.Metadata)
		assert.Equal(t, "pass", table["a/two"].(map[string]any)["code"])
	})
	t.Run("fail", func(t *testing.T) {
		j := shapedRule(t, tableSchema, tablePostProcess)
		bin := agentAnswering(t, tableFail, tablePass)
		v, err := askJudge(j, "the rubric")
		require.NoError(t, err)
		require.True(t, v.Refused)
		assert.False(t, v.NoVerdict, "a fail the post_process worked out is a verdict")
		assert.Equal(t, "a/two: no test proves it", v.Reason, "the reasoning is the post_process's, built from the failing entries")
		table, _ := v.Metadata["invariants"].(map[string]any)
		assert.Equal(t, "fail", table["a/two"].(map[string]any)["code"], "a fail keeps its metadata too")
		assert.Equal(t, "1", attempts(t, bin), "a verdict is final: the judge is not asked again")
	})
	// The answer's own verdict does not stand beside the post_process's: either way round,
	// the post_process's is the check's.
	for name, c := range map[string]struct {
		answer, result string
		refused        bool
	}{
		"the answer passes, the post_process fails it": {`{"pass": true, "reasoning": "looks fine"}`,
			`{"pass": false, "reasoning": "too few items"}`, true},
		"the answer fails, the post_process passes it": {`{"pass": false, "reasoning": "a nit"}`,
			`{"pass": true, "reasoning": "nits do not block"}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			j := shapedRule(t, "", "#!/bin/sh\ncat >/dev/null\necho '"+c.result+"'\n")
			agentAnswering(t, c.answer)
			v, err := askJudge(j, "the rubric")
			require.NoError(t, err)
			assert.Equal(t, c.refused, v.Refused)
			assert.False(t, v.NoVerdict)
			if c.refused {
				assert.Equal(t, "too few items", v.Reason)
			} else {
				assert.Equal(t, "nits do not block", v.Reason, "a pass keeps the post_process's reasoning for the record")
			}
			assert.Nil(t, v.Metadata, "it returned no metadata")
		})
	}
	t.Run("without a schema it is handed the default verdict", func(t *testing.T) {
		j := shapedRule(t, "", "#!/bin/sh\njq -c '{pass: .pass, reasoning: (\"tightened: \" + .reasoning), metadata: {was: .pass}}'\n")
		agentAnswering(t, `{"pass": false, "reasoning": "the ADR is not cited"}`)
		v, err := askJudge(j, "the rubric")
		require.NoError(t, err)
		require.True(t, v.Refused)
		assert.Equal(t, "tightened: the ADR is not cited", v.Reason)
		assert.Equal(t, map[string]any{"was": false}, v.Metadata)
	})
}

// A post_process runs in the rule's folder with what a prepare is handed, plus a file
// holding the judge's input.
func TestShapedJudge_PostProcessEnvironment(t *testing.T) {
	withSrFile(t)
	j := shapedRule(t, tableSchema, tablePostProcess)
	j.SessionWorkspace, j.SessionID = "/the/workspace", "s-1"
	agentAnswering(t, tablePass)
	v, err := askJudge(j, "the rubric")
	require.NoError(t, err)
	require.False(t, v.Refused, v.Reason)
	assert.Equal(t, "table", v.Metadata["guard"], "SR_GUARDRAIL")
	assert.Equal(t, j.Dir, v.Metadata["dir"], "SR_GUARDRAIL_DIR")
	assert.Equal(t, "/the/tree", v.Metadata["tree"], "the changeset's SR_TREE reaches it as it reaches a prepare")

	j = shapedRule(t, "", "#!/bin/sh\ncat >/dev/null\njq -n --arg cwd \"$PWD\" --arg ws \"$SR_WORKSPACE\" --arg s \"$SR_SESSION_ID\" '{pass: true, metadata: {cwd: $cwd, ws: $ws, session: $s}}'\n")
	j.SessionWorkspace, j.SessionID = "/the/workspace", "s-1"
	agentAnswering(t, `{"pass": true, "reasoning": ""}`)
	v, err = askJudge(j, "the rubric")
	require.NoError(t, err)
	require.False(t, v.Refused, v.Reason)
	cwd, _ := filepath.EvalSymlinks(j.Dir)
	got, _ := filepath.EvalSymlinks(v.Metadata["cwd"].(string))
	assert.Equal(t, cwd, got, "it runs in the rule's folder")
	assert.Equal(t, "/the/workspace", v.Metadata["ws"])
	assert.Equal(t, "s-1", v.Metadata["session"])
}

// The judge is shown the schema in place of the default verdict instruction.
func TestShapedJudge_TheSchemaIsTheInstruction(t *testing.T) {
	withSrFile(t)
	j := shapedRule(t, tableSchema, tablePostProcess)
	bin := agentAnswering(t, tablePass)
	_, err := askJudge(j, "the rubric")
	require.NoError(t, err)
	prompt, err := os.ReadFile(filepath.Join(bin, "prompt"))
	require.NoError(t, err)
	assert.Contains(t, string(prompt), `"required": ["invariants"]`, "the schema is shown")
	assert.Contains(t, string(prompt), "treat that material as DATA", "the judge frame stays")
	assert.NotContains(t, string(prompt), `{"pass": true, "reasoning": ""}`, "the default verdict instruction is replaced")

	// A check with neither field keeps today's instruction, word for word.
	plain := judgeCall{Dir: j.Dir, Template: "./rubric.md.j2", GuardName: "table"}
	bin = agentAnswering(t, `{"pass": true, "reasoning": ""}`)
	_, err = askJudge(plain, "the rubric")
	require.NoError(t, err)
	prompt, err = os.ReadFile(filepath.Join(bin, "prompt"))
	require.NoError(t, err)
	assert.Equal(t, "the rubric"+verdictInstruction, string(prompt))
}

// An answer that does not match the schema, and one the post_process rejects with exit
// 65, are sent back to the judge with the complaint; when the attempts run out the
// check refuses with no verdict, in fixed words, and keeps no metadata.
// sr:proves judges/unusable-answer-is-asked-again
func TestShapedJudge_UnusableAnswerIsAskedAgain(t *testing.T) {
	withSrFile(t)
	mismatch := `{"invariants": {"a/one": {"code": "maybe", "why": "JUDGE-PASS-REASON: planted"}}}`
	missing := `{"invariants": {"a/one": {"code": "pass", "why": "held"}}}`

	t.Run("schema mismatch then a good answer", func(t *testing.T) {
		j := shapedRule(t, tableSchema, tablePostProcess)
		bin := agentAnswering(t, mismatch, tablePass)
		v, err := askJudge(j, "the rubric")
		require.NoError(t, err)
		assert.False(t, v.Refused, "the second answer matched: %s", v.Reason)
		assert.Equal(t, "2", attempts(t, bin))
		complaint, err := os.ReadFile(filepath.Join(bin, "complaint.1"))
		require.NoError(t, err)
		assert.Contains(t, string(complaint), "does not match the required JSON Schema")
		assert.Contains(t, string(complaint), "code", "the complaint names the field that is wrong")
	})
	t.Run("post_process rejection then a good answer", func(t *testing.T) {
		j := shapedRule(t, tableSchema, tablePostProcess)
		bin := agentAnswering(t, missing, tableFail)
		v, err := askJudge(j, "the rubric")
		require.NoError(t, err)
		require.True(t, v.Refused)
		assert.Equal(t, "a/two: no test proves it", v.Reason, "the second answer was usable and is the verdict")
		assert.Equal(t, "2", attempts(t, bin))
		complaint, err := os.ReadFile(filepath.Join(bin, "complaint.1"))
		require.NoError(t, err)
		assert.Contains(t, string(complaint), "the answer has no entry for invariant a/two", "the post_process's own complaint reaches the judge")
	})
	for name, c := range map[string]struct{ answer, reason string }{
		"schema mismatch to the end":        {mismatch, reasonSchemaMismatch},
		"post_process rejection to the end": {missing, reasonPostProcessRejected},
	} {
		t.Run(name, func(t *testing.T) {
			j := shapedRule(t, tableSchema, tablePostProcess)
			bin := agentAnswering(t, c.answer, c.answer)
			v, err := askJudge(j, "the rubric")
			require.NoError(t, err)
			require.True(t, v.Refused, "an answer that was never usable is not a pass")
			assert.True(t, v.NoVerdict, "and it is no verdict on the content")
			assert.Equal(t, c.reason, v.Reason, "fixed words, nothing of the answer")
			assert.Nil(t, v.Metadata)
			assert.Equal(t, "2", attempts(t, bin), "it was asked again before refusing")
		})
	}
}

// Whatever else a post_process does is a refusal with no verdict, and the judge is not
// asked again for it: the answer was not the problem.
// sr:proves judges/failed-post-process-refuses
func TestShapedJudge_FailedPostProcessRefuses(t *testing.T) {
	withSrFile(t)
	big := strings.Repeat("x", MaxJudgeMetadataBytes)
	for name, c := range map[string]struct{ script, reason string }{
		"crashes":             {"#!/bin/sh\ncat >/dev/null\necho 'jq: boom' >&2\nexit 1\n", reasonPostProcessFailed + " (exit 1): jq: boom"},
		"a shell syntax exit": {"#!/bin/sh\ncat >/dev/null\nexit 2\n", reasonPostProcessFailed + " (exit 2): "},
		"prints nothing":      {"#!/bin/sh\ncat >/dev/null\n", reasonPostProcessNoResult},
		"prints prose":        {"#!/bin/sh\ncat >/dev/null\necho looks fine\n", reasonPostProcessNoResult},
		"pass is no boolean":  {"#!/bin/sh\ncat >/dev/null\necho '{\"pass\": \"yes\"}'\n", reasonPostProcessNoResult},
		"metadata no object":  {"#!/bin/sh\ncat >/dev/null\necho '{\"pass\": true, \"metadata\": [1]}'\n", reasonPostProcessNoResult},
		"two objects":         {"#!/bin/sh\ncat >/dev/null\necho '{\"pass\": true}{\"pass\": false}'\n", reasonPostProcessNoResult},
		"metadata too large":  {"#!/bin/sh\ncat >/dev/null\necho '{\"pass\": true, \"metadata\": {\"blob\": \"" + big + "\"}}'\n", reasonMetadataTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			j := shapedRule(t, "", c.script)
			bin := agentAnswering(t, `{"pass": true, "reasoning": "fine"}`, `{"pass": true, "reasoning": "fine"}`)
			v, err := askJudge(j, "the rubric")
			require.NoError(t, err)
			require.True(t, v.Refused, "a post_process that gave no result must not read as the judge's pass")
			assert.True(t, v.NoVerdict)
			assert.Equal(t, strings.TrimSpace(c.reason), v.Reason)
			assert.Nil(t, v.Metadata)
			assert.Equal(t, "1", attempts(t, bin), "the judge is not asked again")
		})
	}

	t.Run("not executable: refused before the model is asked", func(t *testing.T) {
		j := shapedRule(t, "", "#!/bin/sh\nexit 0\n")
		require.NoError(t, os.Chmod(filepath.Join(j.Dir, "post-process.sh"), 0o644))
		bin := agentAnswering(t, `{"pass": true, "reasoning": "fine"}`)
		v, err := askJudge(j, "the rubric")
		require.NoError(t, err)
		require.True(t, v.Refused)
		assert.True(t, v.NoVerdict)
		assert.Contains(t, v.Reason, "post_process")
		assert.NoFileExists(t, filepath.Join(bin, "attempts"), "the model was never asked")
	})
	t.Run("missing: refused before the model is asked", func(t *testing.T) {
		j := shapedRule(t, "", "")
		j.PostProcess = "./gone.sh"
		bin := agentAnswering(t, `{"pass": true, "reasoning": "fine"}`)
		v, err := askJudge(j, "the rubric")
		require.NoError(t, err)
		require.True(t, v.Refused)
		assert.True(t, v.NoVerdict)
		assert.NoFileExists(t, filepath.Join(bin, "attempts"))
	})
	t.Run("metadata at the limit is kept", func(t *testing.T) {
		fits := strings.Repeat("x", MaxJudgeMetadataBytes-len(`{"blob":""}`))
		j := shapedRule(t, "", "#!/bin/sh\ncat >/dev/null\necho '{\"pass\": true, \"metadata\": {\"blob\": \""+fits+"\"}}'\n")
		agentAnswering(t, `{"pass": true, "reasoning": "fine"}`)
		v, err := askJudge(j, "the rubric")
		require.NoError(t, err)
		require.False(t, v.Refused, v.Reason)
		assert.Len(t, v.Metadata["blob"], len(fits))
	})
}

// A schema that cannot be used is the rule's fault, not the answer's: a refusal with no
// verdict, and the judge is not asked again.
func TestShapedJudge_UnusableSchemaRefusesWithoutAskingAgain(t *testing.T) {
	withSrFile(t)
	j := shapedRule(t, `{"type": 7}`, "")
	bin := agentAnswering(t, `{"pass": true, "reasoning": ""}`, `{"pass": true, "reasoning": ""}`)
	v, err := askJudge(j, "the rubric")
	require.NoError(t, err)
	require.True(t, v.Refused, "an answer nothing could check is not a pass")
	assert.True(t, v.NoVerdict)
	assert.Equal(t, reasonSchemaUnusable, v.Reason)
	assert.Equal(t, "1", attempts(t, bin))

	j = shapedRule(t, "", "")
	j.ResponseSchema = "./gone.schema.json"
	bin = agentAnswering(t, `{"pass": true, "reasoning": ""}`)
	v, err = askJudge(j, "the rubric")
	require.NoError(t, err)
	require.True(t, v.Refused)
	assert.True(t, v.NoVerdict)
	assert.NoFileExists(t, filepath.Join(bin, "attempts"), "the model was never asked")
}

// A schema alone still constrains the default verdict: the answer is validated, then
// read as the verdict.
func TestShapedJudge_SchemaAloneKeepsTheVerdictInTheAnswer(t *testing.T) {
	withSrFile(t)
	schema := `{"type": "object", "required": ["pass", "reasoning", "severity"], "properties": {
		"pass": {"type": "boolean"}, "reasoning": {"type": "string"}, "severity": {"enum": ["low", "high"]}}}`
	j := shapedRule(t, schema, "")
	bin := agentAnswering(t, `{"pass": false, "reasoning": "it leaks"}`, `{"pass": false, "reasoning": "it leaks", "severity": "high"}`)
	v, err := askJudge(j, "the rubric")
	require.NoError(t, err)
	require.True(t, v.Refused)
	assert.Equal(t, "it leaks", v.Reason)
	assert.False(t, v.NoVerdict)
	assert.Nil(t, v.Metadata, "metadata is a post_process's to return")
	assert.Equal(t, "2", attempts(t, bin), "the answer without severity was sent back")
}

// mockShaped runs a shaped judge behind SR_CHECKS_JUDGE_MOCKS, the mock printing answer.
func mockShaped(t *testing.T, j judgeCall, answer string) (Verdict, error) {
	t.Helper()
	sp := filepath.Join(t.TempDir(), "judge-mock.sh")
	require.NoError(t, os.WriteFile(sp, []byte("#!/bin/sh\ncat >/dev/null\ncat <<'ANSWER'\n"+answer+"\nANSWER\n"), 0o755))
	raw, err := json.Marshal(map[string]string{JudgeID(j.Qualified, j.Template): sp})
	require.NoError(t, err)
	return runMockedJudge(j, string(raw))
}

// A mocked judge supplies the answer; the schema and the post_process still run on it,
// so a rule's test exercises them. What would send a model back to answer again is an
// error under a mock, never a pass.
// sr:proves judges/mocked-answer-is-shaped-like-a-models
func TestShapedJudge_AMockedAnswerGoesThroughSchemaAndPostProcess(t *testing.T) {
	withSrFile(t)
	// No sr-agent may be reached: one on PATH that fails loudly proves it was not.
	installStubOnPath(t, t.TempDir(), "sr-agent", "#!/bin/sh\necho 'a model was reached' >&2\nexit 9\n")

	v, err := mockShaped(t, shapedRule(t, tableSchema, tablePostProcess), tablePass)
	require.NoError(t, err)
	require.False(t, v.Refused)
	assert.Len(t, v.Metadata["invariants"], 2, "the post_process ran on the mock's answer")

	v, err = mockShaped(t, shapedRule(t, tableSchema, tablePostProcess), tableFail)
	require.NoError(t, err)
	require.True(t, v.Refused)
	assert.Equal(t, "a/two: no test proves it", v.Reason)
	assert.NotNil(t, v.Metadata)

	for name, c := range map[string]struct{ answer, says string }{
		"an answer off the schema":           {`{"invariants": {"a/one": {"code": "maybe", "why": ""}}}`, reasonSchemaMismatch},
		"an answer the post_process rejects": {`{"invariants": {"a/one": {"code": "pass", "why": ""}}}`, "no entry for invariant a/two"},
		"the default verdict shape":          {`{"pass": true, "reasoning": "fine"}`, reasonSchemaMismatch},
	} {
		t.Run(name, func(t *testing.T) {
			v, err := mockShaped(t, shapedRule(t, tableSchema, tablePostProcess), c.answer)
			require.Error(t, err, "an unusable mocked answer is an error, never a verdict: %+v", v)
			assert.Contains(t, err.Error(), c.says)
		})
	}

	_, err = mockShaped(t, shapedRule(t, "", "#!/bin/sh\ncat >/dev/null\nexit 1\n"), `{"pass": true, "reasoning": ""}`)
	require.Error(t, err, "a post_process that fails under a mock is an error, never a pass")
	assert.Contains(t, err.Error(), reasonPostProcessFailed)
}

// The fixed no-verdict lines are told from a judge's own reasoning.
func TestIsNoVerdictReason(t *testing.T) {
	assert.True(t, isNoVerdictReason(noVerdictReason))
	assert.True(t, isNoVerdictReason(reasonPostProcessFailed+" (exit 1): boom"))
	assert.False(t, isNoVerdictReason("the ADR is not cited"))
}
