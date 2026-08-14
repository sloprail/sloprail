package filemod

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/module"
)

// The symlink escape must be refused IDENTICALLY on every path into reportable
// and in every spelling, which is a 2x2 that took three rounds to fill in one
// cell at a time.
//
//	                 absolute spelling      relative spelling
//	tool write       round 1                round 2
//	shell command    round 2                never tested until here
//
// Each round fixed the cell it was looking at and left a sibling holding the
// identical defect, because the two branches of reportable and the two callers
// of it were never made to answer the same question at once. That is what this
// table is: one assertion over the whole product, so a fix to any one cell
// cannot again look complete while another still admits the file.
//
// The property is narrow and is the whole point. A path resolving OUTSIDE the
// repository must never be reported as a clean repository-relative one —
// `escape/id_rsa` is a spelling `path startsWith "escape/"` admits, and a hook
// joining it onto its own root then reads the outside file with nothing
// reporting a problem. Reported absolutely, no project-relative matcher admits
// it, which is the honest answer: the write is outside every rule's subject.
//
// Verified by mutation: making the relative branch lexical again turns the
// tool-write AND command relative cells red together, reporting exactly
// "escape/id_rsa" — the string round 2 measured.
func TestR3_EscapeRefusedOnEveryPathAndSpelling(t *testing.T) {
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "id_rsa"), []byte("KEY"), 0o600))

	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.NoError(t, os.Symlink(outside, filepath.Join(realRoot, "escape")))

	m := New()

	cases := map[string]struct{ args string }{
		"toolwrite-relative": {`{"file_path":"escape/id_rsa","content":"x"}`},
		"toolwrite-absolute": {`{"file_path":"` + filepath.Join(realRoot, "escape", "id_rsa") + `","content":"x"}`},
		"command-relative":   {`{"command":"printf x > escape/id_rsa"}`},
		"command-absolute":   {`{"command":"printf x > ` + filepath.Join(realRoot, "escape", "id_rsa") + `"}`},
		"command-rm-rel":     {`{"command":"rm escape/id_rsa"}`},
		"command-rm-abs":     {`{"command":"rm ` + filepath.Join(realRoot, "escape", "id_rsa") + `"}`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// cwd must be the repo root for the command path's relative stat.
			t.Chdir(realRoot)
			evs, _ := m.Extract(module.Input{
				module.InputPhase:   module.PhasePre,
				module.InputPayload: fakePending{tool: "T", root: realRoot, args: json.RawMessage(tc.args)},
			})
			for _, e := range evs {
				p, _ := e.Fields[FieldPath].(string)
				t.Logf("  kind=%s path=%q", e.Kind, p)
				assert.False(t, p == "escape/id_rsa",
					"a file OUTSIDE the repo must never be reported as a clean repo-relative path — "+
						"`path startsWith \"escape/\"` would admit it and a hook would join it onto its own root")
			}
		})
	}
}
