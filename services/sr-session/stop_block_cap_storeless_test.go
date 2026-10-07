package main

import (
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// The refusal count survives a session store that cannot be opened: a harness
// with no cap of its own would otherwise be sent round again for ever.
func TestRefusalCounterFileCountsWithoutAStore(t *testing.T) {
	counter := refusalCounterFile(filepath.Join(t.TempDir(), "state.db.stop-refusals"))
	cmd := &cobra.Command{}
	if n := stopRefusals(cmd, counter); n != 0 {
		t.Fatalf("a missing counter file reads %d, want 0", n)
	}
	for i := 0; i < 3; i++ {
		countStopRefusal(cmd, counter)
	}
	if n := stopRefusals(cmd, counter); n != 3 {
		t.Fatalf("three refusals counted %d", n)
	}
	resetStopRefusals(cmd, counter)
	if n := stopRefusals(cmd, counter); n != 0 {
		t.Fatalf("a reset counter reads %d, want 0", n)
	}
}
