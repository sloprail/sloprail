package harness

import "encoding/json"

// The canonical tool vocabulary.
//
// Every harness spells its tools its own way; the engine's modules (filemod,
// commandmod, tag, ...) read tool names and arguments in ONE spelling, the one
// Claude Code established, and an adapter's ParseHook maps its harness onto it.
// A tool a harness has that is not below is passed through under its own name and
// is simply not a write or a command as far as any module knows.
//
//	Bash          a shell command; arguments {"command": "<line>"}.
//	Write         create or overwrite a file; {"file_path", "content"}.
//	Edit          replace text in a file; {"file_path", "old_string", "new_string", "replace_all"?}.
//	MultiEdit     several sequential Edits; {"file_path", "edits": [{"old_string","new_string"}...]}.
//	NotebookEdit  edit a notebook cell; {"notebook_path", "new_source", ...}.
//	Read          read a file; {"file_path"}. Never a write.
//
// The same names, and the canonical input keys, are what a rule sees on a PreToolUse
// event (`tool`, `input`): beside the file and shell tools above, WebFetch ({"url",
// "prompt"?}), WebSearch ({"query"}), Agent (spawn a sub-agent: {"prompt",
// "description"?, "subagent_type"?}), Grep, Glob, and an MCP tool as
// mcp__<server>__<tool> on every harness. A harness renames its own tools onto these
// with Canonicalize and a table; what it cannot map keeps its own name and input, and
// HookInput.NativeToolName always holds the name the harness reported.
//
// A harness whose write tool cannot be expressed in those argument shapes (a
// multi-file patch) reports the effects in HookInput.Files instead and keeps
// its own tool name.
//
// These are the keys commandmod.HarnessCommandTools and HarnessWriteTools hold; a
// test there pins the two together.
const (
	ToolBash         = "Bash"
	ToolWrite        = "Write"
	ToolEdit         = "Edit"
	ToolMultiEdit    = "MultiEdit"
	ToolNotebookEdit = "NotebookEdit"
	ToolRead         = "Read"
)

// ToolAlias maps one harness-native tool onto a canonical one.
type ToolAlias struct {
	// Name is the canonical tool name.
	Name string
	// Keys renames the input's top-level keys, native key to canonical key. A key
	// not named stays as it is.
	Keys map[string]string
}

// Canonicalize returns the canonical name and input for a native tool, by a harness's
// own alias table. A tool the table does not name is returned as it came: the
// adapter lists what it knows, and everything else is a tool no module reads.
// An input that is not a JSON object is returned as it came, with the name mapped.
func Canonicalize(native string, input json.RawMessage, aliases map[string]ToolAlias) (string, json.RawMessage) {
	a, ok := aliases[native]
	if !ok {
		return native, input
	}
	if len(a.Keys) == 0 {
		return a.Name, input
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(input, &obj) != nil || obj == nil {
		return a.Name, input
	}
	renamed := make(map[string]json.RawMessage, len(obj))
	for k, v := range obj {
		if to, ok := a.Keys[k]; ok {
			k = to
		}
		renamed[k] = v
	}
	out, err := json.Marshal(renamed)
	if err != nil {
		return a.Name, input
	}
	return a.Name, out
}

// NativeName is the value for HookInput.NativeToolName: the name the harness reported,
// or "" when it is already the canonical one.
func NativeName(native, canonical string) string {
	if native == canonical {
		return ""
	}
	return native
}
