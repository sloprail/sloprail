package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
)

func cmdEvent(cwd string, argvs ...[]string) event.Event {
	var invs []any
	for _, argv := range argvs {
		a := make([]any, len(argv))
		for i, s := range argv {
			a[i] = s
		}
		invs = append(invs, map[string]any{"bin": argv[0], "argv": a, "cwd": cwd})
	}
	return event.Event{Kind: "PreCommandInvoke", Fields: map[string]any{"raw": "x", "invocations": invs}}
}

func TestRepairsContext(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, ".sloprail", "context", "refactoring")
	c := declaration.Context{Name: "refactoring", Dir: dir, Enter: "./enter.sh", Exit: "./exit.sh"}
	file := func(kind, path string) event.Event {
		return event.Event{Kind: kind, Fields: map[string]any{"path": path}}
	}
	tool := event.Event{Kind: "PreToolUse", Fields: map[string]any{"tool": "Write"}}
	cases := []struct {
		name string
		ev   event.Event
		want bool
	}{
		{"edit of the enter script", file(declaration.KindPreFileUpdate, ".sloprail/context/refactoring/enter.sh"), true},
		{"write of a sibling in the folder", file(declaration.KindPreFileCreate, ".sloprail/context/refactoring/helper.sh"), true},
		{"absolute path in the folder", file(declaration.KindPreFileUpdate, filepath.Join(dir, "enter.sh")), true},
		{"write elsewhere", file(declaration.KindPreFileCreate, "notes.md"), false},
		{"look-alike folder outside", file(declaration.KindPreFileUpdate, ".sloprail/context/refactoring-evil/enter.sh"), false},
		{"dotdot out of the folder", file(declaration.KindPreFileUpdate, ".sloprail/context/refactoring/../other/enter.sh"), false},
		{"delete in the folder is no repair", file(declaration.KindPreFileDelete, ".sloprail/context/refactoring/enter.sh"), false},
		{"chmod +x enter", cmdEvent(".", []string{"chmod", "+x", ".sloprail/context/refactoring/enter.sh"}), true},
		{"chmod 755 both scripts", cmdEvent(".", []string{"chmod", "755", ".sloprail/context/refactoring/enter.sh", ".sloprail/context/refactoring/exit.sh"}), true},
		{"chmod -R flag", cmdEvent(".", []string{"chmod", "-R", "+x", ".sloprail/context/refactoring"}), true},
		{"chmod after cd", cmdEvent(".sloprail/context/refactoring", []string{"chmod", "+x", "enter.sh"}), true},
		{"chmod on another file", cmdEvent(".", []string{"chmod", "+x", "build.sh"}), false},
		{"chmod on the script and another file", cmdEvent(".", []string{"chmod", "+x", ".sloprail/context/refactoring/enter.sh", "build.sh"}), false},
		{"chmod on a look-alike", cmdEvent(".", []string{"chmod", "+x", ".sloprail/context/refactoring-evil/enter.sh"}), false},
		{"chmod -x removes the bit", cmdEvent(".", []string{"chmod", "-x", ".sloprail/context/refactoring/enter.sh"}), false},
		{"chmod u-x removes the bit", cmdEvent(".", []string{"chmod", "u-x", ".sloprail/context/refactoring/enter.sh"}), false},
		{"chmod ug+x", cmdEvent(".", []string{"chmod", "ug+x", ".sloprail/context/refactoring/enter.sh"}), true},
		{"chmod octal with an execute bit", cmdEvent(".", []string{"chmod", "0755", ".sloprail/context/refactoring/enter.sh"}), true},
		{"chmod octal without one", cmdEvent(".", []string{"chmod", "644", ".sloprail/context/refactoring/enter.sh"}), false},
		{"chmod sticky octal is no execute bit", cmdEvent(".", []string{"chmod", "1644", ".sloprail/context/refactoring/enter.sh"}), false},
		{"chmod four-digit octal with an execute bit", cmdEvent(".", []string{"chmod", "0755", ".sloprail/context/refactoring/enter.sh"}), true},
		{"sr-file edit, a --k=v flag after the path", cmdEvent(".", []string{"sr-file", "edit", ".sloprail/context/refactoring/enter.sh", "--path=/other/file"}), false},
		{"sr-file edit, a bare -- after the path", cmdEvent(".", []string{"sr-file", "edit", ".sloprail/context/refactoring/enter.sh", "--", "/other/file"}), false},
		{"chmod +r only", cmdEvent(".", []string{"chmod", "+r", ".sloprail/context/refactoring/enter.sh"}), false},
		{"chmod --reference", cmdEvent(".", []string{"chmod", "--reference", "other.sh", ".sloprail/context/refactoring/enter.sh"}), false},
		{"chmod --reference=F", cmdEvent(".", []string{"chmod", "--reference=other.sh", ".sloprail/context/refactoring/enter.sh"}), false},
		{"chmod with an unknown flag", cmdEvent(".", []string{"chmod", "-f", "+x", ".sloprail/context/refactoring/enter.sh"}), false},
		{"chmod with no path", cmdEvent(".", []string{"chmod", "+x"}), false},
		{"sr-file edit, path hidden behind a flag value", cmdEvent(".", []string{"sr-file", "edit", "--old-string", ".sloprail/context/refactoring/enter.sh", "--new-string", "x", "/some/other/file"}), false},
		{"sr-file edit, flag value then path", cmdEvent(".", []string{"sr-file", "edit", ".sloprail/context/refactoring/enter.sh", "--new-string", "x", "/some/other/file"}), false},
		{"sr-file delete is no repair", cmdEvent(".", []string{"sr-file", "delete", ".sloprail/context/refactoring/enter.sh"}), false},
		{"rm of the script", cmdEvent(".", []string{"rm", ".sloprail/context/refactoring/enter.sh"}), false},
		{"mv of the script", cmdEvent(".", []string{"mv", ".sloprail/context/refactoring/enter.sh", "x"}), false},
		{"chmod chained with something else", cmdEvent(".", []string{"chmod", "+x", ".sloprail/context/refactoring/enter.sh"}, []string{"rm", "-rf", "src"}), false},
		{"unknown cwd", cmdEvent("", []string{"chmod", "+x", ".sloprail/context/refactoring/enter.sh"}), false},
		{"sr-file edit on the script", cmdEvent(".", []string{"sr-file", "edit", ".sloprail/context/refactoring/enter.sh", "--force"}), true},
		{"sr-file write elsewhere", cmdEvent(".", []string{"sr-file", "write", "notes.md"}), false},
		{"an arbitrary command naming the script", cmdEvent(".", []string{"sed", "-i", "", "1i x", ".sloprail/context/refactoring/enter.sh"}), false},
		{"cat of the script", cmdEvent(".", []string{"cat", ".sloprail/context/refactoring/enter.sh"}), false},
		{"no invocations", cmdEvent("."), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, repairsContext([]event.Event{tool, tc.ev}, c, ws))
		})
	}
}

// The call as a whole is judged: the tool event alone is no repair, and a repair next to another
// action is none.
func TestRepairsContext_WholeCall(t *testing.T) {
	ws := t.TempDir()
	c := declaration.Context{Name: "r", Dir: filepath.Join(ws, "ctx"), Enter: "./enter.sh"}
	tool := event.Event{Kind: "PreToolUse", Fields: map[string]any{"tool": "Skill"}}
	fix := event.Event{Kind: declaration.KindPreFileUpdate, Fields: map[string]any{"path": "ctx/enter.sh"}}
	other := event.Event{Kind: declaration.KindPreFileCreate, Fields: map[string]any{"path": "x.md"}}
	assert.False(t, repairsContext([]event.Event{tool}, c, ws), "a call that does nothing to a file or command is no repair")
	assert.True(t, repairsContext([]event.Event{tool, fix}, c, ws))
	assert.False(t, repairsContext([]event.Event{tool, fix, other}, c, ws))
	assert.False(t, repairsContext(nil, c, ws))
}
