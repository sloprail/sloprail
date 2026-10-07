package codex

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/harness"
)

// hookPayload is what Codex puts on a hook's standard input (recorded: harness-mocks
// codex-mock/snapshots/runs/*/payloads.jsonl; documented at
// https://developers.openai.com/codex/hooks#common-input-fields).
//
// Every payload names the session, the working directory, the event and the model;
// a tool event adds turn_id, tool_name, tool_input and tool_use_id (PostToolUse also
// tool_response); Stop adds stop_hook_active and last_assistant_message. transcript_path
// is null for an ephemeral session. A hook fired inside a sub-agent carries agent_id
// and agent_type, and ITS transcript_path is the SUB-AGENT's own rollout while
// session_id stays the parent's; SubagentStop alone reports agent_transcript_path
// beside the parent's transcript_path.
type hookPayload struct {
	HookEventName       string          `json:"hook_event_name"`
	SessionID           string          `json:"session_id"`
	TurnID              string          `json:"turn_id"`
	TranscriptPath      *string         `json:"transcript_path"`
	AgentTranscriptPath string          `json:"agent_transcript_path"`
	AgentID             string          `json:"agent_id"`
	AgentType           string          `json:"agent_type"`
	Source              string          `json:"source"`
	Cwd                 string          `json:"cwd"`
	ToolName            string          `json:"tool_name"`
	ToolUseID           string          `json:"tool_use_id"`
	ToolInput           json.RawMessage `json:"tool_input"`
	StopHookActive      bool            `json:"stop_hook_active"`
}

// ParseHook implements harness.HookWire.
//
// Codex's shell tool is already "Bash" in a hook payload with {"command": "<line>"},
// the canonical shape. Its file-writing tool is not: apply_patch carries a patch in
// tool_input.command, so a PreToolUse for it is reported as the file effects the
// patch would have (harness.HookInput.Files), under its own tool name.
func (Harness) ParseHook(r io.Reader) harness.HookInput {
	b, err := io.ReadAll(r)
	if err != nil || len(b) == 0 {
		return harness.HookInput{}
	}
	var p hookPayload
	_ = json.Unmarshal(b, &p)

	in := harness.HookInput{
		Event:          p.HookEventName,
		SessionID:      p.SessionID,
		Source:         p.Source,
		Cwd:            p.Cwd,
		AgentID:        p.AgentID,
		AgentType:      p.AgentType,
		ToolName:       p.ToolName,
		ToolUseID:      p.ToolUseID,
		ToolInput:      p.ToolInput,
		StopHookActive: p.StopHookActive,
	}
	transcript := ""
	if p.TranscriptPath != nil {
		transcript = *p.TranscriptPath
	}
	switch {
	case p.AgentTranscriptPath != "":
		// SubagentStop: both the parent's record and the sub-agent's own.
		in.TranscriptPath, in.AgentTranscriptPath = transcript, p.AgentTranscriptPath
	case p.AgentID != "":
		// Anything else fired inside a sub-agent: transcript_path IS its own record.
		// The parent's is left unnamed (LocateTranscript finds it from the session id).
		in.AgentTranscriptPath = transcript
	default:
		in.TranscriptPath = transcript
	}
	if p.HookEventName == "PreToolUse" && patchTools[p.ToolName] {
		in.Files = patchEffects(p.ToolInput, p.Cwd)
	}
	return in
}

// patchTools are the names a hook reports Codex's patch tool under; its matcher
// documentation lists apply_patch, Edit and Write as aliases.
var patchTools = map[string]bool{"apply_patch": true, "Edit": true, "Write": true}

// maxPatchRead bounds the file a patch is applied to in a hook the agent waits on.
const maxPatchRead = 4 << 20

// patchEffects works out what a PreToolUse patch would do to each file it names.
// A file whose result cannot be derived (a hunk that finds no place, a file too
// large or unreadable) is reported with ResultKnown false: the write is real and its
// path known, its bytes are not, and a gate must fail closed on that rather than
// judge an empty string.
func patchEffects(input json.RawMessage, cwd string) []harness.FileEffect {
	var in struct {
		Command string `json:"command"`
		Input   string `json:"input"`
	}
	if json.Unmarshal(input, &in) != nil {
		return nil
	}
	text := in.Command
	if text == "" {
		text = in.Input
	}
	ops, ok := parsePatch(text)
	if !ok {
		return nil
	}
	abs := func(p string) string {
		if p == "" || filepath.IsAbs(p) || cwd == "" {
			return p
		}
		return filepath.Join(cwd, p)
	}
	var out []harness.FileEffect
	for _, op := range ops {
		path := abs(op.path)
		switch op.kind {
		case opAdd:
			content := ""
			if len(op.add) > 0 {
				content = strings.Join(op.add, "\n") + "\n"
			}
			out = append(out, harness.FileEffect{Kind: harness.FileCreate, Path: path, NewContent: content, ResultKnown: true})
		case opDelete:
			out = append(out, harness.FileEffect{Kind: harness.FileDelete, Path: path})
		case opUpdate:
			result, known := "", false
			if before, ok := readSmall(path); ok {
				result, known = op.apply(before)
			}
			if op.moveTo == "" {
				out = append(out, harness.FileEffect{Kind: harness.FileUpdate, Path: path, NewContent: result, ResultKnown: known})
				continue
			}
			out = append(out,
				harness.FileEffect{Kind: harness.FileDelete, Path: path},
				harness.FileEffect{Kind: harness.FileCreate, Path: abs(op.moveTo), NewContent: result, ResultKnown: known})
		}
	}
	return out
}

func readSmall(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPatchRead {
		return "", false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// RenderHook implements harness.HookWire. Codex reads the contract Claude Code does
// (https://developers.openai.com/codex/hooks#pretooluse): a deny is a PreToolUse
// permissionDecision, a Stop refusal is decision:"block". PreToolUse output carrying
// `continue` (or any decision other than block) makes the hook fail, so none is ever
// written.
func (Harness) RenderHook(w io.Writer, resp harness.HookResponse) error {
	return harness.RenderHookJSON(w, resp)
}
