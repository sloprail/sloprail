package cursor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hookScript is the plugin's Cursor entrypoint; a hook entry whose command runs it is
// sloprail's, which is how an install finds its own entries to refresh or remove.
const hookScript = "sr-session-hook-cursor.sh"

// projectHooks are the events sloprail registers in the project's .cursor/hooks.json
// rather than relying on the plugin for, and the argument each passes to the script.
//
// MEASURED in Cursor's TUI (harness-mocks #287, tui-plugins and tui-plugins-event-gating):
// a plugin's `stop` hook never fires, and a local plugin loads late, after sessionStart
// has already been delivered, so its sessionStart hook never runs. Project hooks
// (<project>/.cursor/hooks.json) fire for both. preCompact is here too: the recordings
// that show it fire (tui-manual-compaction, compaction-transcript-continuity) register it
// in the project, and none shows a plugin's preCompact firing. The other events stay in
// the plugin.
var projectHooks = []struct {
	event, arg string
	timeout    int
}{
	{"sessionStart", "start", 3600},
	{"stop", "stop", 3600},
	{"preCompact", "post-tool", 60},
}

// HooksPath is the project's hooks file.
func HooksPath(project string) string { return filepath.Join(project, ".cursor", "hooks.json") }

// InstallProjectHooks implements harness.ProjectHooks: it merges sloprail's stop and
// sessionStart entries (running pluginDir's hooks/sr-session-hook-cursor.sh) into
// <project>/.cursor/hooks.json. Entries that are not sloprail's, and any other key of the
// file, are kept as they are; sloprail's own are replaced, so running it again, or after
// the plugin moved, leaves one of each. A hooks file that is not a JSON object is an
// error, not overwritten.
func (Harness) InstallProjectHooks(project, pluginDir string) error {
	script := filepath.Join(pluginDir, "hooks", hookScript)
	return editHooks(project, func(hooks map[string][]json.RawMessage) {
		for _, h := range projectHooks {
			kept := withoutSloprail(hooks[h.event])
			entry, _ := json.Marshal(map[string]any{"command": shellWord(script) + " " + h.arg, "timeout": h.timeout})
			hooks[h.event] = append(kept, entry)
		}
	})
}

// RemoveProjectHooks implements harness.ProjectHooks: it takes sloprail's entries out of
// the project's hooks file, leaves every other entry, and deletes the file only when
// nothing of anyone's remains.
func (Harness) RemoveProjectHooks(project string) error {
	return editHooks(project, func(hooks map[string][]json.RawMessage) {
		for _, h := range projectHooks {
			if kept := withoutSloprail(hooks[h.event]); len(kept) > 0 {
				hooks[h.event] = kept
			} else {
				delete(hooks, h.event)
			}
		}
	})
}

func editHooks(project string, edit func(hooks map[string][]json.RawMessage)) error {
	path := HooksPath(project)
	raw, err := os.ReadFile(path)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return err
	}
	doc := map[string]json.RawMessage{}
	if !missing && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s is not a JSON object, left as it is: %w", path, err)
		}
	}
	hooks := map[string][]json.RawMessage{}
	if h, ok := doc["hooks"]; ok {
		if err := json.Unmarshal(h, &hooks); err != nil {
			return fmt.Errorf(`%s: "hooks" is not an object of lists, left as it is: %w`, path, err)
		}
	}
	edit(hooks)
	if len(hooks) == 0 {
		delete(doc, "hooks")
	} else {
		doc["hooks"], _ = json.Marshal(hooks)
		if _, ok := doc["version"]; !ok {
			doc["version"] = json.RawMessage("1")
		}
	}
	// nothing of anyone's left (a version alone is not content)
	if _, hasVersion := doc["version"]; len(doc) == 0 || (len(doc) == 1 && hasVersion) {
		if missing {
			return nil
		}
		return os.Remove(path)
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if !missing && bytes.Equal(bytes.TrimSpace(raw), bytes.TrimSpace(out)) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// withoutSloprail drops the entries that run sloprail's Cursor hook script.
func withoutSloprail(entries []json.RawMessage) []json.RawMessage {
	var kept []json.RawMessage
	for _, e := range entries {
		var c struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(e, &c) == nil && strings.Contains(c.Command, hookScript) {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

// shellWord quotes s for the shell Cursor runs a hook command with, only when it must be.
func shellWord(s string) string {
	if !strings.ContainsAny(s, " \t'\"$`\\()&;|<>*?[]{}!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
