package e2e

import (
	"strings"
	"testing"
)

// A judge template naming a filter the engine does not have fails closed — and
// the load check says so before any rule runs.
//
// gonja renders an unknown filter as its error object's name
// (`<*errors.errorString>`) and reports no error, so without a check a typo in a
// judge template hands the model garbage and the judge rules on it anyway. The
// engine checks every filter name before rendering: the judge refuses, naming
// the template and the filter, and `sr-session start < /dev/null` reports the
// template while the rule still loads (it refuses at run time, fail-closed).

const typoGate = `on:
  - event: Stop
checks:
  - judge: ./is-done.md.j2
`

const typoTemplate = `Is the work done?

<summary>{{ event.kind | uppper }}</summary>
`

const cleanTemplate = `Is the work done?

<summary>{{ event.kind | upper }}</summary>
`

func TestT032_11_UnknownFilterInJudgeTemplateFailsClosed(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "work-is-done", typoGate, map[string]string{"is-done.md.j2": typoTemplate})
	e.CommitAll(proj, "the guards")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	load := e.CLI(proj, "session", "start")
	if !strings.Contains(load.Output, "is-done.md.j2") || !strings.Contains(load.Output, `"uppper"`) {
		t.Errorf("the load check did not report the unknown filter in the judge template:\n%s", load.Output)
	}
	if strings.Contains(load.Output, "not loaded") {
		t.Errorf("a template problem must not unload the rule — it refuses at run time:\n%s", load.Output)
	}

	e.Run(proj, "s-032-11", "finish up", Turns("done", Bash("b1", "echo hi")))
	blocks := strings.Join(e.BlockingErrorsFrom(proj, "s-032-11", "Stop"), "\n")
	if blocks == "" {
		t.Fatalf("a judge whose template names an unknown filter passed (the stub said pass) instead of refusing")
	}
	if !strings.Contains(blocks, "is-done.md.j2") || !strings.Contains(blocks, `"uppper"`) {
		t.Errorf("the refusal does not name the template and the filter:\n%s", blocks)
	}
}

// The control: the same rule with a filter the engine has loads clean, and the
// judge's verdict decides.
func TestT032_12_KnownFilterRendersAndLoadsClean(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "work-is-done", typoGate, map[string]string{"is-done.md.j2": cleanTemplate})
	e.CommitAll(proj, "the guards")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	load := e.CLI(proj, "session", "start")
	if strings.Contains(load.Output, "is-done.md.j2") {
		t.Errorf("the load check reported a template that renders:\n%s", load.Output)
	}
	e.Run(proj, "s-032-12", "finish up", Turns("done", Bash("b1", "echo hi")))
	if blocks := e.BlockingErrorsFrom(proj, "s-032-12", "Stop"); len(blocks) != 0 {
		t.Errorf("a judge with a known filter and a pass verdict refused:\n%v", blocks)
	}
}
