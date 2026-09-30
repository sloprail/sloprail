package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// fileguard_deletions: a file-guard's `deletions:` key, end to end through the
// mock — which file events a guard is handed when the agent creates, edits and
// DELETES a tracked file.
//
// A file-guard has no `on:`; it binds to a file's state, and before this key the
// engine handed it every change to a matching path, delete included. A deleted
// file has no end state and no newContent, so most guards had nothing to judge
// and each waved deletes through on its own (or, worse, refused them for lacking
// content). `deletions:` is one axis with three values:
//
//   - absent / `skip` (the default) — the guard is not run on PreFileDelete or
//     PostFileDelete;
//   - `include` — creates, updates and deletes (the old behaviour);
//   - `only` — deletes only.
//
// The same vocabulary drives both halves of a rule: a file-guard's `deletions:`
// filters the Post events it is asked about at Stop, and a gate binds to
// PreFileWrite and/or PreFileDelete to prevent. Observed through a ledger each
// check appends `<kind> <path>` to, and, for a gate that refuses deletes, through
// whether the `rm` is denied.

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

var (
	Turns = harness.Turns
	Write = harness.Write
	Bash  = harness.Bash
)

// New is harness.New with the plugin's authoring file-guards switched off: this package
// is about other rules, and the authoring guards would judge the rules' own files.
func New(t *testing.T) *harness.Env { return harness.New(t, harness.WithoutShippedFileGuards()) }
