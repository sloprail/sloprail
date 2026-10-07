package harness

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// renderClaude is one action as the stream-json line a10n-claude-mock reads: the
// Claude Code transcript record the agent's step lands as. It is the only place
// a Turn becomes Claude's JSON.
func renderClaude(a Action) (string, error) {
	switch a.Kind {
	case ActWrite:
		return toolUse(a.ID, "Write", map[string]string{"file_path": a.Path, "content": a.Content}), nil
	case ActEdit:
		return toolUse(a.ID, "Edit", map[string]string{"file_path": a.Path, "old_string": a.Old, "new_string": a.New}), nil
	case ActBash:
		return toolUse(a.ID, "Bash", map[string]string{"command": a.Command}), nil
	case ActSkill:
		return toolUse(a.ID, "Skill", map[string]string{"skill": a.Text}), nil
	case ActToolUse:
		return toolUse(a.ID, a.Tool, a.Input), nil
	case ActWebFetch:
		return renderClaude(Action{Kind: ActToolUseJSON, ID: a.ID, Tool: "WebFetch",
			Raw: fmt.Sprintf(`{"url":%s,"prompt":%s,"mock_result":{"result":"(the fetched page)"}}`, jsonStr(a.Text), jsonStr(a.Text2))})
	case ActToolUseJSON:
		return fmt.Sprintf(`{"type":"assistant","uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`,
			"e2e-turn-"+a.ID, a.ID, a.Tool, a.Raw), nil
	case ActSay:
		return fmt.Sprintf(
			`{"type":"assistant","uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"text","text":%s}]}}`,
			"e2e-turn-"+a.ID, jsonStr(a.Text)), nil
	case ActSayWrite:
		return sayWithTool(a.ID, a.Text, "Write", map[string]string{"file_path": a.Path, "content": a.Content}), nil
	case ActSayBash:
		return sayWithTool(a.ID, a.Text, "Bash", map[string]string{"command": a.Command}), nil
	case ActCall:
		// The tool_use, with a top-level `id` as the marker anchor so the block's own
		// `id` stays clean and equal to `id`.
		input := a.Raw
		if input == "" {
			input = inputObject(a.Tool, a.Input)
		}
		return fmt.Sprintf(
			`{"type":"assistant","id":%q,"uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`,
			a.ID+"#u", "e2e-turn-"+a.ID+"u", a.ID, a.Tool, input), nil
	case ActArtifact:
		return fmt.Sprintf(
			`{"type":"user","id":%q,"uuid":%q,"toolUseResult":%s,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":"the tool produced its result"}]}}`,
			a.ID+"#r", "e2e-turn-"+a.ID+"r", a.Raw, a.ID), nil
	case ActToolResult:
		return fmt.Sprintf(
			`{"type":"user","id":%q,"uuid":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":%s}]}}`,
			a.ID+"#r", "e2e-turn-"+a.ID, a.ID, jsonStr(a.Text)), nil
	case ActToolResultWithText:
		return fmt.Sprintf(
			`{"type":"user","id":%q,"uuid":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":%s},{"type":"text","text":%s}]}}`,
			a.ID+"#r", "e2e-turn-"+a.ID, a.ID, jsonStr(a.Text), jsonStr(a.Text2)), nil
	case ActAnswer:
		content := `The user answered: `
		for i, p := range a.QA {
			if i > 0 {
				content += `, `
			}
			content += `"` + p[0] + `"="` + p[1] + `"`
		}
		content += `. Read the answers carefully — they may request clarification, changes, or that you not proceed.`
		return fmt.Sprintf(
			`{"type":"user","id":%q,"uuid":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":%s}]}}`,
			a.ID+"#a", "e2e-turn-"+a.ID, a.ID, jsonStr(content)), nil
	case ActDispatch:
		input := map[string]string{
			"description":   "delegated work",
			"prompt":        a.Text,
			"subagent_type": "general-purpose",
			"script":        a.Script,
		}
		if a.Isolation != "" {
			input["isolation"] = a.Isolation
		}
		return toolUse(a.ID, "Agent", input), nil
	case ActDispatchNoParent:
		input := map[string]string{
			"description":   "delegated work",
			"prompt":        a.Text,
			"subagent_type": "general-purpose",
			"script":        a.Script,
		}
		var ib strings.Builder
		ib.WriteByte('{')
		for i, k := range sortedKeys(input) {
			if i > 0 {
				ib.WriteByte(',')
			}
			fmt.Fprintf(&ib, "%q:%s", k, jsonStr(input[k]))
		}
		ib.WriteByte('}')
		// Top-level `id` is the marker anchor; the tool_use block's own `id` is empty so
		// the mock records an empty toolUseId and the parent stays underivable.
		return fmt.Sprintf(
			`{"type":"assistant","id":%q,"uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"tool_use","id":"","name":"Agent","input":%s}]}}`,
			a.ID+"#np", "e2e-turn-"+a.ID, ib.String()), nil
	case ActBashBatch:
		blocks := make([]string, 0, len(a.Commands))
		for i, cmd := range a.Commands {
			blockID := a.ID
			if i > 0 {
				blockID = fmt.Sprintf("%s-%d", a.ID, i)
			}
			blocks = append(blocks, fmt.Sprintf(
				`{"type":"tool_use","id":%q,"name":"Bash","input":{"command":%s}}`,
				blockID, jsonStr(cmd)))
		}
		return fmt.Sprintf(
			`{"type":"assistant","uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[%s]}}`,
			"e2e-turn-"+a.ID, strings.Join(blocks, ",")), nil
	case ActCompact:
		if a.UnwrittenParent {
			return fmt.Sprintf(`{"type":"compact","id":%q,"summary":"compacted","logical_parent":"never-written-%s"}`, a.ID, a.ID), nil
		}
		return fmt.Sprintf(`{"type":"compact","id":%q,"summary":"compacted"}`, a.ID), nil
	}
	return "", fmt.Errorf("harness: claude cannot render action kind %d", a.Kind)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
	return fmt.Sprintf(`{"type":"assistant","uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`,
		"e2e-turn-"+id, id, name, inputObject(name, input))
}

// inputObject is a tool's input as a JSON object, each value typed by inputValue.
func inputObject(name string, input map[string]string) string {
	var ib strings.Builder
	ib.WriteByte('{')
	first := true
	for _, k := range sortedKeys(input) {
		v := input[k]
		if !first {
			ib.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&ib, "%q:%s", k, inputValue(name, k, v))
	}
	ib.WriteByte('}')
	return ib.String()
}

// typedInputs lists, for the tools the mock models, the inputs real Claude Code
// sends as JSON booleans or numbers rather than strings. A scenario still gives
// every input as a string; toolUse writes these as the typed JSON a real agent
// sends, so a hook or the mock sees `"run_in_background": true`, not `"true"`.
// Tools the mock does not model yet are left as strings.
var typedInputs = map[string]map[string]string{
	"Bash":  {"run_in_background": "bool", "timeout": "number", "dangerouslyDisableSandbox": "bool"},
	"Read":  {"limit": "number", "offset": "number"},
	"Edit":  {"replace_all": "bool"},
	"Agent": {"run_in_background": "bool"},
	"Task":  {"run_in_background": "bool"},
}

// inputValue is v as the JSON value of the named input of tool: typed when
// typedInputs says so and v parses as that type, a string otherwise.
func inputValue(tool, key, v string) string {
	switch typedInputs[tool][key] {
	case "bool":
		if b, err := strconv.ParseBool(v); err == nil {
			return strconv.FormatBool(b)
		}
	case "number":
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return strconv.FormatInt(n, 10)
		}
	}
	return jsonStr(v)
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

// sayWithTool renders one assistant turn whose content is a two-block list: a
// text block, then a tool_use. The tool_use carries the turn's id (so the mock's
// marker machinery fires it once) and the entry a uuid (so transcript.Read does
// not skip it), the same invariants toolUse and Say each keep for their single
// block.
func sayWithTool(id, prose, name string, input map[string]string) string {
	var ib strings.Builder
	ib.WriteByte('{')
	first := true
	for _, k := range sortedKeys(input) {
		v := input[k]
		if !first {
			ib.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&ib, "%q:%s", k, jsonStr(v))
	}
	ib.WriteByte('}')
	return fmt.Sprintf(
		`{"type":"assistant","uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"text","text":%s},{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`,
		"e2e-turn-"+id, jsonStr(prose), id, name, ib.String())
}
