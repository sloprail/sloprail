package harness

import (
	"errors"
	"fmt"
	"testing"
)

// ActionKind is what a Turn makes the agent do, in no harness's words.
type ActionKind int

const (
	// ActWrite writes Content to Path (replacing the file).
	ActWrite ActionKind = iota + 1
	// ActEdit replaces Old with New in the file at Path.
	ActEdit
	// ActBash runs Command in the shell.
	ActBash
	// ActSay is prose only: Text.
	ActSay
	// ActSayWrite is Text and an ActWrite in one message.
	ActSayWrite
	// ActSayBash is Text and an ActBash in one message.
	ActSayBash
	// ActDispatch hands Text to a sub-agent running the scenario at Script, in
	// its own worktree when Isolation is "worktree".
	ActDispatch
	// ActSkill loads the skill named Text.
	ActSkill
	// ActToolUse invokes the tool Tool with Input (Background: as a background task).
	ActToolUse

	// The kinds below are Claude Code's records, written as the agent's transcript
	// holds them: another harness has no counterpart, and reports them unsupported.

	// ActToolUseJSON invokes Tool with the raw JSON input Raw.
	ActToolUseJSON
	// ActWebFetch fetches the page at Text, asking Text2 of it.
	ActWebFetch
	// ActCall is a tool call whose result a later turn supplies: Tool with Input
	// (or the raw JSON input Raw).
	ActCall
	// ActArtifact is the result of the ActCall of the same ID, Raw the tool's
	// structured output.
	ActArtifact
	// ActToolResult is a tool's result text, Text, for the call of the same ID.
	ActToolResult
	// ActToolResultWithText is a tool result, Text, that also carries typed words, Text2.
	ActToolResultWithText
	// ActAnswer is a person's answers to a question: QA pairs of question and answer.
	ActAnswer
	// ActDispatchNoParent is ActDispatch whose dispatching call names no id.
	ActDispatchNoParent
	// ActBashBatch runs Commands as one message of several calls.
	ActBashBatch
	// ActCompact compacts the context (UnwrittenParent: naming a parent no file holds).
	ActCompact
)

// Action is one assistant step: a kind and the fields that kind uses. A Driver
// renders it in its harness's own script dialect.
type Action struct {
	Kind ActionKind
	// ID is the turn's id, unique among a session's turns: a turn fires once per
	// conversation, which it tells by finding its id in the record.
	ID string

	Path, Content, Old, New string
	Command                 string
	Text, Text2             string
	Tool                    string
	Input                   map[string]string
	Raw                     string
	Commands                []string
	QA                      [][2]string
	Script, Isolation       string
	Background              bool
	UnwrittenParent         bool
}

// fingerprint names an action's content, for ids derived from what a scenario does.
func (a Action) fingerprint() string { return fmt.Sprintf("%#v", a) }

// UnsupportedError is a scenario step the selected harness cannot take. A test
// that meets it is skipped, naming the step, not run as something else.
type UnsupportedError struct {
	Harness string
	Step    string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("harness %s cannot take the scenario step %s", e.Harness, e.Step)
}

// SkipIfUnsupported skips the test when err is an UnsupportedError and reports
// whether it did.
func SkipIfUnsupported(t testing.TB, err error) bool {
	t.Helper()
	var u *UnsupportedError
	if errors.As(err, &u) {
		t.Skipf("%v", u)
		return true
	}
	return false
}
