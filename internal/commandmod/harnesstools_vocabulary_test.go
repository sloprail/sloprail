package commandmod

import (
	"testing"

	"github.com/sloprail/sloprail/internal/harness"
)

// The lists here and the canonical vocabulary documented in internal/harness are one
// fact stated twice; this pins them together so an adapter mapping onto the
// vocabulary maps onto what the modules actually read.
func TestToolListsAreTheCanonicalVocabulary(t *testing.T) {
	writes := map[string]bool{harness.ToolWrite: true, harness.ToolEdit: true, harness.ToolMultiEdit: true, harness.ToolNotebookEdit: true}
	for name := range writes {
		if !HarnessWriteTools[name] {
			t.Errorf("%s is canonical but not a write tool here", name)
		}
	}
	for name := range HarnessWriteTools {
		if !writes[name] {
			t.Errorf("%s is a write tool here but not in the canonical vocabulary", name)
		}
	}
	if len(HarnessCommandTools) != 1 || !HarnessCommandTools[harness.ToolBash] {
		t.Errorf("command tools %v, want exactly Bash", HarnessCommandTools)
	}
	if HarnessWriteTools[harness.ToolRead] || HarnessCommandTools[harness.ToolRead] {
		t.Error("Read must never be a write or a command")
	}
}
