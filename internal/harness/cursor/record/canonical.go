package record

import (
	"encoding/json"

	"github.com/sloprail/sloprail/internal/harness"
)

// toolNames maps Cursor's transcript tool names onto the canonical ones
// (harness/tools.go); a tool absent here keeps its own name.
var toolNames = map[string]string{"Shell": harness.ToolBash, "StrReplace": harness.ToolEdit}

// inputKeys maps Cursor's transcript argument spellings (`path`, `contents`) onto the
// canonical ones (`file_path`, `content`). Cursor's hook payloads already spell Write
// canonically; only the transcript differs (recorded: file-tools transcript).
var inputKeys = map[string]string{"path": "file_path", "contents": "content"}

// canonicalMessage rewrites the tool_use blocks of a message into the canonical
// vocabulary. A message that is not the {content:[...]} shape, or has nothing to
// rewrite, is returned unchanged.
func canonicalMessage(raw json.RawMessage) json.RawMessage {
	var msg map[string]json.RawMessage
	if json.Unmarshal(raw, &msg) != nil {
		return raw
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(msg["content"], &blocks) != nil {
		return raw
	}
	changed := false
	for _, b := range blocks {
		var typ, name string
		_ = json.Unmarshal(b["type"], &typ)
		if typ != "tool_use" {
			continue
		}
		_ = json.Unmarshal(b["name"], &name)
		if to, ok := toolNames[name]; ok {
			b["name"], _ = json.Marshal(to)
			changed = true
		}
		var in map[string]json.RawMessage
		if json.Unmarshal(b["input"], &in) != nil {
			continue
		}
		for from, to := range inputKeys {
			if v, ok := in[from]; ok {
				if _, taken := in[to]; !taken {
					in[to] = v
					delete(in, from)
					changed = true
				}
			}
		}
		b["input"], _ = json.Marshal(in)
	}
	if !changed {
		return raw
	}
	msg["content"], _ = json.Marshal(blocks)
	out, err := json.Marshal(msg)
	if err != nil {
		return raw
	}
	return out
}
