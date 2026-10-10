package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A judge check with a `response_schema` and a `post_process`, through the compiled
// binaries: sr-checks runs the real sr-agent, whose agent is a stand-in answering in the
// rule's own shape; sr-file validates the answer; the rule's post-process.sh works out the
// verdict and returns the per-invariant table as metadata, which the stored verdict keeps.

const (
	tableRule = `match: "docs/**"
checks:
  - prepare: ./prepare.sh
    judge: ./rubric.md.j2
    response_schema: ./response.schema.json
    post_process: ./post-process.sh
`
	tablePrepare = `#!/bin/sh
cat >/dev/null
echo '{"additionalContext": {"ids": ["docs/dated", "docs/cited"]}}'
`
	tableRubric = `Judge each of these invariants over the change: {{ additionalContext.ids | tojson }}
{{ change }}
`
	tableSchema = `{
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
}
`
	// Every id prepare handed the judge must be answered, or the answer is unusable (65);
	// pass is "everything passes"; the reasoning names the failing entries; the table is
	// the metadata.
	tablePostProcess = `#!/bin/sh
answer="$(cat)"
for id in $(jq -r '.additionalContext.ids[]' "$SR_JUDGE_INPUT"); do
  printf '%s' "$answer" | jq -e --arg id "$id" '.invariants | has($id)' >/dev/null || {
    echo "no entry for the invariant $id: answer for every id you were given" >&2
    exit 65
  }
done
printf '%s' "$answer" | jq -c '{
  pass: ([.invariants[] | .code, .tests] | all(. == "pass")),
  reasoning: ([.invariants | to_entries[] | select(.value.code == "fail" or .value.tests == "fail") | "\(.key): \(.value.why)"] | join("; ")),
  metadata: {invariants: .invariants}
}'
`
	tableAllPass = `{"invariants": {"docs/dated": {"code": "pass", "tests": "pass", "why": "a date is there"}, "docs/cited": {"code": "pass", "tests": "pass", "why": "the ADR is cited"}}}`
	tableOneFail = `{"invariants": {"docs/dated": {"code": "pass", "tests": "pass", "why": "a date is there"}, "docs/cited": {"code": "pass", "tests": "fail", "why": "no test covers the citation"}}}`
)

var tableFiles = map[string]string{
	"prepare.sh": tablePrepare, "rubric.md.j2": tableRubric,
	"response.schema.json": tableSchema, "post-process.sh": tablePostProcess,
}

// answersInTurn is a stand-in for the judge's agent that answers with the files
// <dir>/answer.1, answer.2, ... in turn (the last one again once they run out), counting
// its calls in <dir>/calls and keeping each prompt as <dir>/prompt.<n>.
func answersInTurn(t *testing.T, e *Env, answers ...string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	for i, a := range answers {
		if err := os.WriteFile(filepath.Join(dir, "answer."+string(rune('1'+i))), []byte(a+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.InstallJudgeScript(`#!/bin/sh
dir=` + shellQ(dir) + `
echo call >> "$dir/calls"
n="$(wc -l < "$dir/calls" | tr -d ' ')"
tmp="$dir/prompt.$n"
cat > "$tmp"
for arg in "$@"; do printf '%s\n' "$arg" >> "$tmp"; done
out="$(sed -n 's/.*Write your answer to the file \([^ ]*\)\. .*/\1/p' "$tmp" | tail -1)"
a="$dir/answer.$n"
[ -f "$a" ] || a="$(ls "$dir"/answer.* | tail -1)"
[ -n "$out" ] && cat "$a" > "$out"
exit 0
`)
	return dir
}

func shellQ(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func agentCalls(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "calls"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "call\n")
}

// MetaRow is one `sr-checks show --json` / `verify --json` row, with its metadata.
type MetaRow struct {
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Source   string `json:"source"`
	Reason   string `json:"reason"`
	Metadata struct {
		Invariants map[string]struct {
			Code, Tests, Why string
		} `json:"invariants"`
	} `json:"metadata"`
}

// judgeRow is the judge step among the rows a --json listing printed.
func judgeRow(t *testing.T, out string) MetaRow {
	t.Helper()
	start := strings.Index(out, "[")
	if start < 0 {
		t.Fatalf("no JSON listing in:\n%s", out)
	}
	var rows []MetaRow
	dec := json.NewDecoder(strings.NewReader(out[start:]))
	if err := dec.Decode(&rows); err != nil {
		t.Fatalf("unreadable JSON listing: %v\n%s", err, out)
	}
	for _, r := range rows {
		if strings.Contains(r.Kind, ":judge:") {
			return r
		}
	}
	t.Fatalf("no judge step among the rows:\n%s", out)
	return MetaRow{}
}

func tableProject(t *testing.T) (e *Env, proj, base string) {
	t.Helper()
	e, proj = session(t)
	e.FileGuard(proj, "docs", tableRule, tableFiles)
	base = e.CommitAll(proj, "the rule")
	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	e.CommitAll(proj, "add a")
	return e, proj, base
}

// T001_40: the judge's first answer misses the schema and is sent back; its second matches
// and the post_process fails it. The refusal carries the post_process's reasoning, the
// stored verdict keeps the per-invariant table, and show, verify and log read it back
// without the judge being asked again.
// sr:proves judges/unusable-answer-is-asked-again
// sr:proves judges/post-process-decides-the-verdict
// sr:proves cache/stored-verdict-keeps-metadata
func TestT001_40_SchemaAndPostProcessDecideAndTheMetadataIsStored(t *testing.T) {
	e, proj, base := tableProject(t)
	agent := answersInTurn(t, e, `{"pass": false, "reasoning": "the citation has no test"}`, tableOneFail)

	run := checks(e, proj, "run", "--base", base, "--head", "HEAD")
	if run.Code != 1 {
		t.Fatalf("run exited %d, want 1 (the post_process failed the answer):\n%s", run.Code, run.Output)
	}
	contains(t, run.Output, "docs/cited: no test covers the citation")
	if strings.Contains(run.Output, "a date is there") {
		t.Fatalf("the metadata was shown in the refusal; only the reasoning is:\n%s", run.Output)
	}
	if n := agentCalls(t, agent); n != 2 {
		t.Fatalf("the judge was asked %d times, want 2 (the off-schema answer is sent back once)", n)
	}
	first, err := os.ReadFile(filepath.Join(agent, "prompt.1"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, string(first), `"required": ["invariants"]`, `["docs/dated","docs/cited"]`)
	second, err := os.ReadFile(filepath.Join(agent, "prompt.2"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, string(second), "does not match the required JSON Schema", "invariants")

	for _, verb := range []string{"show", "verify"} {
		res := checks(e, proj, verb, "--json", "--base", base, "--head", "HEAD")
		row := judgeRow(t, res.Output)
		if row.Status != "fail" {
			t.Fatalf("%s: the judge step is %q, want fail:\n%s", verb, row.Status, res.Output)
		}
		cited := row.Metadata.Invariants["docs/cited"]
		if cited.Code != "pass" || cited.Tests != "fail" || cited.Why != "no test covers the citation" || len(row.Metadata.Invariants) != 2 {
			t.Fatalf("%s --json does not carry the stored per-invariant table:\n%s", verb, res.Output)
		}
	}

	log := checks(e, proj, "log", "--json")
	var entry struct {
		Status   string `json:"status"`
		Metadata map[string]struct {
			Invariants map[string]struct{ Tests string } `json:"invariants"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(log.Output)), &entry); err != nil {
		t.Fatalf("log --json is not one verdict line: %v\n%s", err, log.Output)
	}
	if got := entry.Metadata["check[0]:judge:./rubric.md.j2"].Invariants["docs/cited"].Tests; entry.Status != "fail" || got != "fail" {
		t.Fatalf("log --json does not carry the metadata under its step:\n%s", log.Output)
	}

	again := checks(e, proj, "run", "--base", base, "--head", "HEAD")
	if again.Code != 1 || !strings.Contains(again.Output, "docs/cited: no test covers the citation") {
		t.Fatalf("the stored fail was not replayed: exit %d\n%s", again.Code, again.Output)
	}
	if n := agentCalls(t, agent); n != 2 {
		t.Fatalf("the replay asked the judge again (%d calls in all)", n)
	}
}

// T001_41: an answer in which everything passes is a pass, and its table is stored too.
// sr:proves judges/post-process-decides-the-verdict
// sr:proves cache/stored-verdict-keeps-metadata
func TestT001_41_APassKeepsItsMetadata(t *testing.T) {
	e, proj, base := tableProject(t)
	agent := answersInTurn(t, e, tableAllPass)
	if run := checks(e, proj, "run", "--base", base, "--head", "HEAD"); run.Code != 0 {
		t.Fatalf("run exited %d, want 0:\n%s", run.Code, run.Output)
	}
	if n := agentCalls(t, agent); n != 1 {
		t.Fatalf("the judge was asked %d times, want 1", n)
	}
	res := checks(e, proj, "show", "--json", "--base", base, "--head", "HEAD")
	row := judgeRow(t, res.Output)
	if row.Status != "pass" || row.Metadata.Invariants["docs/dated"].Why != "a date is there" || len(row.Metadata.Invariants) != 2 {
		t.Fatalf("show --json does not carry a pass's metadata:\n%s", res.Output)
	}
}

// T001_42: an answer the post_process keeps rejecting (an invariant left out) ends with no
// verdict: run refuses in fixed words and stores nothing, so the next run asks again.
// sr:proves judges/unusable-answer-is-asked-again
func TestT001_42_AnAnswerNeverUsableIsNoVerdict(t *testing.T) {
	e, proj, base := tableProject(t)
	agent := answersInTurn(t, e, `{"invariants": {"docs/dated": {"code": "pass", "tests": "pass", "why": "a date is there"}}}`)
	run := checks(e, proj, "run", "--base", base, "--head", "HEAD")
	if run.Code != 1 {
		t.Fatalf("run exited %d, want 1:\n%s", run.Code, run.Output)
	}
	contains(t, run.Output, "post_process rejected the judge's answer as unusable")
	if n := agentCalls(t, agent); n != 2 {
		t.Fatalf("the judge was asked %d times, want 2", n)
	}
	second, err := os.ReadFile(filepath.Join(agent, "prompt.2"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, string(second), "no entry for the invariant docs/cited")
	verify := checks(e, proj, "verify", "--base", base, "--head", "HEAD")
	contains(t, verify.Output, "not judged yet")
	checks(e, proj, "run", "--base", base, "--head", "HEAD")
	if n := agentCalls(t, agent); n != 4 {
		t.Fatalf("the next run did not ask the judge again (%d calls in all, want 4)", n)
	}
}

// T001_43: behind SR_CHECKS_JUDGE_MOCKS the mock supplies the judge's answer, and the
// schema and the post_process still run on it: a rule's own test exercises both, and no
// model is reached.
// sr:proves judges/mocked-answer-is-shaped-like-a-models
func TestT001_43_AMockedJudgeStillGoesThroughSchemaAndPostProcess(t *testing.T) {
	e, proj, base := tableProject(t)
	e.InstallJudgeScript("#!/bin/sh\necho 'a model was reached' >&2\nexit 9\n")
	mock := filepath.Join(t.TempDir(), "judge-mock.sh")
	answer := filepath.Join(t.TempDir(), "answer.json")
	if err := os.WriteFile(mock, []byte("#!/bin/sh\ncat >/dev/null\ncat "+shellQ(answer)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(e.SessionEnv(sessionID), `SR_CHECKS_JUDGE_MOCKS={"file-guard/docs/rubric":"`+mock+`"}`)
	run := func(a string) harness.Result {
		if err := os.WriteFile(answer, []byte(a+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return quiet(e.CLIDirectEnv(proj, env, "sr-checks", "run", "--base", base, "--head", "HEAD"))
	}

	// Off the schema: an error, never a verdict; nothing is stored, so the next answer is judged.
	if r := run(`{"pass": true, "reasoning": "fine"}`); r.Code == 0 || !strings.Contains(r.Output, "did not match the check's response_schema") {
		t.Fatalf("a mocked answer off the schema: exit %d, want an error naming the schema:\n%s", r.Code, r.Output)
	}
	r := run(tableOneFail)
	if r.Code != 1 || !strings.Contains(r.Output, "docs/cited: no test covers the citation") {
		t.Fatalf("the post_process did not decide on the mocked answer: exit %d\n%s", r.Code, r.Output)
	}
	res := quiet(e.CLIDirectEnv(proj, env, "sr-checks", "show", "--json", "--base", base, "--head", "HEAD"))
	if row := judgeRow(t, res.Output); row.Metadata.Invariants["docs/cited"].Tests != "fail" {
		t.Fatalf("the mocked verdict's metadata was not stored:\n%s", res.Output)
	}
	if strings.Contains(r.Output+res.Output, "a model was reached") {
		t.Fatalf("a model was reached behind the mock:\n%s", r.Output)
	}
}

// attemptsOf makes the session's sr-agent the real one with its limit of attempts set to n
// (the engine leaves it at sr-agent's default of 2).
func attemptsOf(e *Env, n string) {
	e.InstallShim("sr-agent", "#!/bin/sh\nexec "+shellQ(e.BinPath("sr-agent"))+" --verify-attempts "+n+" \"$@\"\n")
}

// T001_44: how often an unusable answer is sent back is the judge's limit of attempts, and
// no more. At 1 the first unusable answer ends it; at 3 an answer that is usable only the
// third time is the verdict, and one never usable is asked for exactly three times.
// sr:proves judges/unusable-answer-is-asked-again
func TestT001_44_AnUnusableAnswerIsAskedForUpToTheAttemptsLimit(t *testing.T) {
	offSchema := `{"pass": false, "reasoning": "the citation has no test"}`
	rejected := `{"invariants": {"docs/dated": {"code": "pass", "tests": "pass", "why": "a date is there"}}}`

	t.Run("one attempt: not asked again", func(t *testing.T) {
		e, proj, base := tableProject(t)
		attemptsOf(e, "1")
		agent := answersInTurn(t, e, offSchema, tableAllPass)
		run := checks(e, proj, "run", "--base", base, "--head", "HEAD")
		if run.Code != 1 {
			t.Fatalf("run exited %d, want 1 (the only answer was off the schema):\n%s", run.Code, run.Output)
		}
		contains(t, run.Output, "did not match the check's response_schema")
		if n := agentCalls(t, agent); n != 1 {
			t.Fatalf("the judge was asked %d times, want 1: a usable second answer was waiting, and must not be asked for", n)
		}
	})
	t.Run("three attempts: the third answer is the verdict", func(t *testing.T) {
		e, proj, base := tableProject(t)
		attemptsOf(e, "3")
		agent := answersInTurn(t, e, offSchema, rejected, tableAllPass)
		if run := checks(e, proj, "run", "--base", base, "--head", "HEAD"); run.Code != 0 {
			t.Fatalf("run exited %d, want 0 (the third answer passes; at the default of 2 it is never reached):\n%s", run.Code, run.Output)
		}
		if n := agentCalls(t, agent); n != 3 {
			t.Fatalf("the judge was asked %d times, want 3", n)
		}
		third, err := os.ReadFile(filepath.Join(agent, "prompt.3"))
		if err != nil {
			t.Fatal(err)
		}
		contains(t, string(third), "no entry for the invariant docs/cited")
	})
	t.Run("three attempts: never usable is asked for three times, then no verdict", func(t *testing.T) {
		e, proj, base := tableProject(t)
		attemptsOf(e, "3")
		agent := answersInTurn(t, e, rejected)
		run := checks(e, proj, "run", "--base", base, "--head", "HEAD")
		if run.Code != 1 {
			t.Fatalf("run exited %d, want 1:\n%s", run.Code, run.Output)
		}
		contains(t, run.Output, "post_process rejected the judge's answer as unusable")
		if n := agentCalls(t, agent); n != 3 {
			t.Fatalf("the judge was asked %d times, want exactly 3", n)
		}
		verify := checks(e, proj, "verify", "--base", base, "--head", "HEAD")
		contains(t, verify.Output, "not judged yet")
	})
}
