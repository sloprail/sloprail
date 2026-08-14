package harness

import (
	"fmt"
	"os"
	"strings"
)

// Scenario is what the agent does: one turn per action.
//
// The mock runs it as a shell script that streams Claude Code JSONL. A turn
// fires once — it writes a marker into the tool_use id, and skips itself if
// that marker is already in the conversation so far. That is what makes a
// multi-turn scenario work: the mock re-runs the script after each tool result,
// and each run emits the first turn that has not gone yet.
type Scenario struct {
	turns  []Turn
	result string
}

// Turn is one assistant action.
type Turn struct {
	jsonl string
}

// Turns builds a scenario ending in the given assistant text.
func Turns(finalText string, turns ...Turn) Scenario {
	return Scenario{turns: turns, result: finalText}
}

// Write returns a turn where the agent writes a file.
func Write(id, path, content string) Turn {
	return Turn{jsonl: toolUse(id, "Write", map[string]string{
		"file_path": path,
		"content":   content,
	})}
}

// Bash returns a turn where the agent runs a shell command.
func Bash(id, command string) Turn {
	return Turn{jsonl: toolUse(id, "Bash", map[string]string{"command": command})}
}

// Skill returns a turn where the agent loads a skill.
//
// The mock does not implement the Skill tool and answers with an error, which
// is correct for what this is for: the tool_use record lands in the transcript
// either way, and a rule asking whether the agent reached for a skill is asking
// about the reaching. Whether the skill then loaded is the environment's
// business, not evidence about the agent.
func Skill(id, skill string) Turn {
	return Turn{jsonl: toolUse(id, "Skill", map[string]string{"skill": skill})}
}

// Dispatch returns a turn where the agent delegates work to a sub-agent, which
// then runs the scenario at scriptPath in a session of its own.
//
// isolation is "worktree" to give the sub-agent its own tree — the mock binds a
// real `git worktree add` of the project's HEAD, so the sub-agent's hooks run
// with a genuinely separate working directory — and empty to share the
// dispatching session's. The two are the cases a sub-agent's state has to be
// right for, and they differ in exactly one field, which is the point: sharing
// the tree is meant to be the ordinary path, not a second mechanism.
//
// The script field is the mock's own: real Claude Code lets a model decide what
// the sub-agent does, and a test cannot, so the sub-agent's behaviour is
// supplied the same way the dispatching session's is. Every other field is real:
// description, prompt, subagent_type and isolation are all Agent inputs Claude
// Code emits.
//
// The tool is named Agent, which is what Claude Code emits today. Task is the
// tool's former name — it was renamed to Agent in v2.1.63 and Task kept working
// as an accepted alias — so a hook that only matches Task still fires against
// older sessions but never against current ones. A survey of 8,458 local
// transcripts (94,960 tool_use entries) found 493 Agent and zero Task, so a
// harness that emits Task exercises only the legacy alias and would never prove
// a guardrail fires against the name real sessions carry. Consumers should
// accept both names; the harness emits the one production emits.
func Dispatch(id, prompt, scriptPath, isolation string) Turn {
	input := map[string]string{
		"description":   "delegated work",
		"prompt":        prompt,
		"subagent_type": "general-purpose",
		"script":        scriptPath,
	}
	if isolation != "" {
		input["isolation"] = isolation
	}
	return Turn{jsonl: toolUse(id, "Agent", input)}
}

// Script writes a scenario as a standalone script file and returns its path, for
// handing to Dispatch as what the sub-agent runs.
func (s Scenario) Script(path string) error {
	return os.WriteFile(path, []byte("#!/bin/sh\n"+s.script()+"\n"), 0o755)
}

// script renders the scenario as the shell the mock runs.
//
// Each turn is gated on its own marker being absent from the conversation so
// far, so re-running the script advances rather than repeating. With every turn
// emitted, the scenario finishes.
//
// The marker carries the TURN'S OWN ID, not its index. A bare index is unique
// only within one scenario, and a session that is Run more than once — which is
// how a test settles something and then comes back to it under the same
// conversation — writes its markers into a transcript the next Run reads. With
// `slop-turn-0` already in the file from the first Run, every turn of the
// second looks like it has already fired and the whole scenario emits nothing:
// silently, with no error, and with the test observing an empty second cycle
// that it reads as "the hook did not run". The id is the test's own, so
// distinct scenarios cannot collide unless they deliberately reuse it.
func (s Scenario) script() string {
	var b strings.Builder
	b.WriteString("set -u\nSF=\"${A10N_MOCK_SESSION_FILE:-/dev/null}\"\n")
	b.WriteString("SESS=\"$(cat \"$SF\" 2>/dev/null || true)\"\n")

	for i, t := range s.turns {
		marker := fmt.Sprintf("slop-turn-%d-%s", i, turnID(t.jsonl))
		line := injectMarker(t.jsonl, marker)
		fmt.Fprintf(&b, `if ! printf '%%s' "$SESS" | grep -q %q; then
  printf '%%s\n' %s
  exit 0
fi
`, marker, shQuote(line))
	}
	fmt.Fprintf(&b, `printf '%%s\n' %s`, shQuote(result(s.result)))
	return b.String()
}

// toolUse renders one assistant turn invoking a tool.
//
// The record carries a uuid, keyed off the turn's own id. Without one the line
// is not merely untidy — `transcript.Read` SKIPS every record that has no uuid,
// because Claude Code's own preamble and bookkeeping lines carry none. A turn
// emitted without one therefore never reaches `sr-session query`, so a
// rule reading the trajectory sees an empty session and a test asserting on
// what the agent did passes or fails for reasons that have nothing to do with
// the rule.
//
// The parent is deliberately left off. Nothing this harness drives walks the
// chain — the identity walk reads the seeded ROOT record, which has its own
// uuid and an explicit null parent — and inventing a plausible parent chain
// here would be this file asserting a shape it does not maintain.
func toolUse(id, name string, input map[string]string) string {
	var ib strings.Builder
	ib.WriteByte('{')
	first := true
	for k, v := range input {
		if !first {
			ib.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&ib, "%q:%s", k, jsonStr(v))
	}
	ib.WriteByte('}')
	return fmt.Sprintf(`{"type":"assistant","uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`,
		"e2e-turn-"+id, id, name, ib.String())
}

func result(text string) string {
	return fmt.Sprintf(`{"type":"result","subtype":"success","result":%s,"is_error":false}`, jsonStr(text))
}

// turnID reads the tool_use id a turn was built with, before any marker is
// appended to it. Returns "" for a record carrying no id, which leaves the
// marker as the bare index — the old behaviour, and correct for a scenario run
// once.
func turnID(jsonl string) string {
	const idKey = `"id":"`
	i := strings.Index(jsonl, idKey)
	if i < 0 {
		return ""
	}
	j := i + len(idKey)
	end := strings.IndexByte(jsonl[j:], '"')
	if end < 0 {
		return ""
	}
	return jsonl[j : j+end]
}

// injectMarker appends a marker to the tool_use id, so a turn can tell whether
// it has already fired by looking for it in the conversation.
func injectMarker(jsonl, marker string) string {
	const idKey = `"id":"`
	i := strings.Index(jsonl, idKey)
	if i < 0 {
		return jsonl
	}
	j := i + len(idKey)
	end := strings.IndexByte(jsonl[j:], '"')
	if end < 0 {
		return jsonl
	}
	return jsonl[:j] + jsonl[j:j+end] + "-" + marker + jsonl[j+end:]
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func jsonStr(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}
