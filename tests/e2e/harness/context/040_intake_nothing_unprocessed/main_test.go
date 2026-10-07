package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// This package is the end-to-end for the intake-nothing-unprocessed USE CASE
// (strategy unit 15): a gate bound to Stop that refuses when a user message this
// session is neither mapped to a task under tasks/ nor explicitly skipped. The
// whole thing runs through a10n-claude-mock against the SHIPPED example lifted
// verbatim off disk, so what fires is the plugin's own dispatch reaching the
// shipped gate.yaml + its check.
//
// Two accounting paths, both now delivered by the fixed example:
//   - a TASK: the gate greps tasks/ (anchored on $SR_WORKSPACE — a check's cwd is
//     the guardrail folder, not the repo root) for a reference to the message.
//   - a SKIP: the agent declares `#skip <line>` in prose; the sibling
//     skip-declared context enters on that tag (inside a hook, where `state set`
//     is in scope), logs skip:<transcript>:<line>-<line> to its own registry, and
//     the gate reads it with `state list --owner skip-declared`. The redesign is
//     what makes the skip work at all — the agent cannot run `state set` from its
//     own shell (no SR_GUARDRAIL), so the write had to move behind a context.
//
// The gate stays on EVERY Stop and does NOT require the skip context (it must
// check the residue whether or not a skip was declared); the Stop dispatch order
// (context enters before Stop gates) keeps a this-cycle #skip visible to it.
//
// # Harness note
//
// The mock yields exactly ONE normalized type:"user" entry per session (the
// prompt); its tool_result records carry no uuid and are dropped by transcript
// reading. So each scenario has one accountable user message (line 1), which is
// enough to exercise refuse / task-admit / skip-admit / wrong-line-skip. A residue
// of several distinct user messages cannot be presented through this mock.
var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
	Say   = harness.Say
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// exampleName is the folder the example ships under.
const exampleName = "intake-nothing-unprocessed"

// installExampleTree copies the WHOLE shipped `examples/<name>/.sloprail` tree
// into the project verbatim, recursively, so the test exercises the file a user
// would lift rather than a copy restated in the test.
//
// Read off disk, not restated: a test holding its own copy of the gate.yaml and
// its check would prove that copy works and say nothing about the shipped file,
// and the two would drift the first time either was edited alone. This is the
// "lift the real shipped file" install the task calls for.
//
// The copy is VERBATIM, mode included — every shipped example script now carries
// the execute bit in git (100755), so preserving fi.Mode().Perm() lands a
// runnable hook without the copy having to force it. (An earlier revision
// force-chmod'd .sh to 0755 to paper over some scripts that shipped 0644; that
// packaging inconsistency has since been fixed at the source, so the deviation
// is gone and the install is a plain verbatim copy.)
// The fixture under testdata/ is a verbatim copy of sloprail-community examples/<name>.
func installExampleTree(t *testing.T, projDir, name string) {
	t.Helper()
	src := filepath.Join("testdata", name, "sloprail")
	dst := filepath.Join(projDir, ".sloprail")
	copyExampleTree(t, src, dst)
}

func copyExampleTree(t *testing.T, src, dst string) {
	t.Helper()
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("install example: read %s: %v", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("install example: mkdir %s: %v", dst, err)
	}
	for _, ent := range ents {
		s := filepath.Join(src, ent.Name())
		d := filepath.Join(dst, ent.Name())
		if ent.IsDir() {
			copyExampleTree(t, s, d)
			continue
		}
		body, err := os.ReadFile(s)
		if err != nil {
			t.Fatalf("install example: read %s: %v", s, err)
		}
		info, err := ent.Info()
		if err != nil {
			t.Fatalf("install example: stat %s: %v", s, err)
		}
		if err := os.WriteFile(d, body, info.Mode().Perm()); err != nil {
			t.Fatalf("install example: write %s: %v", d, err)
		}
	}
}
