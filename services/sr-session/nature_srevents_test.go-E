package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
)

func TestEmitGateAndStructure(t *testing.T) {
	f := filepath.Join(t.TempDir(), "ev.jsonl")
	t.Setenv("SR_EVENTS_FILE", f)
	g := declaration.Gate{Name: "no-x", Origin: declaration.Origin{Plugin: "sloprail"}}
	e := event.Event{Kind: "PreCommandInvoke"}
	emitGate(g, e, hookScope{ToolUseID: "t1"}, dispatchcore.Verdict{Refused: true, Reason: "nope"}, nil)
	emitGate(g, e, hookScope{ToolUseID: "t2"}, dispatchcore.Verdict{}, nil)
	emitStructure(false, "outside", "PreFileCreate", "t3")
	b, _ := os.ReadFile(f)
	s := string(b)
	for _, want := range []string{
		`"kind":"GateChecked","rule":"sloprail/no-x","outcome":"refused"`, `"tool_use_id":"t1"`, `"reason":"nope"`,
		`"outcome":"permitted"`, `"kind":"StructureChecked"`, `"tool_use_id":"t3"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in\n%s", want, s)
		}
	}
}
