package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// This package is the end-to-end for the intake-nothing-unprocessed USE CASE
// (strategy unit 15): a gate bound to Stop that refuses when a user message this
// session is neither mapped to a task under tasks/ nor explicitly skipped via
// `sr-session state set skip:<ref> <reason>`. The whole thing runs through
// a10n-claude-mock against the SHIPPED example lifted verbatim off disk, so what
// fires is the plugin's own dispatch reaching the shipped gate.yaml + its check.
//
// Unlike the other five use cases in this wave, this one's registry read and
// write live in the SAME guardrail (the gate itself both reads skip: state and
// is the scope the agent's own `state set skip:` writes under), so it does not
// depend on the cross-guardrail `state list --owner` mechanism the sibling
// examples (interlinking, keyword-coverage, completeness) reach for and which the
// engine does not provide — see those packages' notes.
var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
	Bash  = harness.Bash
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
// # The one deviation from verbatim: the execute bit
//
// A hook script is a program; the engine refuses a check it cannot run, and a
// refusal for un-runnability would make every refusal assertion pass for the
// wrong reason (or a pass assertion pass on a guard that never fired). Some
// shipped example scripts carry the execute bit and some do NOT (measured:
// intake/keyword-coverage/deterministic ship +x; interlinking/research-rigor/
// completeness ship 0644) — an inconsistency in the example packaging, noted in
// the report. Because the execute bit is filesystem metadata git may or may not
// preserve, and the LOGIC under test is the file's CONTENT, this forces every
// copied `.sh` to 0755 — exactly what the harness's own e.Gate/e.Context/
// e.FileGuard helpers do when they write a script. The YAML and any other files
// keep their shipped mode.
func installExampleTree(t *testing.T, projDir, name string) {
	t.Helper()
	src := filepath.Join(repoRoot(t), "examples", name, ".sloprail")
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
		perm := info.Mode().Perm()
		if strings.HasSuffix(ent.Name(), ".sh") {
			perm = 0o755 // a hook script is a program; force it runnable
		}
		if err := os.WriteFile(d, body, perm); err != nil {
			t.Fatalf("install example: write %s: %v", d, err)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}
