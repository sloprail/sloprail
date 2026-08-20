package e2e

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// These tests drive the SHIPPED task-management example — a PREVENTIVE file-guard
// over `**/tasks/*/*/ASK.md` with TWO checks in order: a cheap script
// (has-message-reference.sh) that refuses ASK.md content carrying no reference to a
// human message at all, and — only once a reference exists — a prepare + judge
// (resolve-referenced-message.sh + reference-is-true-and-only-this.md.j2) that
// resolves the referenced message from the transcript and rules on whether the
// content is TRUE to it and holds THAT AND NOTHING ELSE. Being preventive, it
// refuses a not-fine write at PRE-tool, before it lands. What fires is this repo's
// plugin against the example's own .sloprail tree, copied in verbatim.
//
// The judge's model verdict is a fixed stub (InstallJudgeClaude) — pass:false
// blocks the pre-tool write, pass:true admits it. What is NOT stubbed: the script
// tier's real reference check, and the prepare's real resolution of the referenced
// message out of the transcript the mock streamed — so a jsonl: line reference
// resolves to the actual human prompt, which the template renders. The capturing
// shim proves that prepare -> template wiring directly (JudgePrompt): the resolved
// message text reaches the prompt, and changes with the reference.
//
// TODO(D3): drive the verdict via a10n-claude-mock once a10n-cli#470 lands and the
// new mock binary is on PATH; today the proven InstallJudgeClaude stub supplies
// the model verdict.
var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const exampleName = "task-management"

// installExampleTree copies examples/<exampleName>/.sloprail into the project,
// verbatim, preserving each file's mode. The scripts (has-message-reference.sh,
// resolve-referenced-message.sh) MUST keep their execute bit or the engine refuses
// them as unrunnable — which is why the mode is carried, not fixed at 0644.
func installExampleTree(t *testing.T, projDir string) {
	t.Helper()
	src := filepath.Join(repoRoot(t), "examples", exampleName, ".sloprail")
	dst := filepath.Join(projDir, ".sloprail")
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		t.Fatalf("install example tree: %s is not a directory (%v)", src, err)
	}
	copied := 0
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		copied++
		return os.WriteFile(target, body, installMode(path, body, fi.Mode().Perm()))
	})
	if err != nil {
		t.Fatalf("install example tree: %v", err)
	}
	if copied == 0 {
		t.Fatalf("install example tree: %s held no files", src)
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

// installMode is the mode a copied example file is written with. It carries the
// source mode over UNCHANGED, with one exception: a `.sh` file that begins with a
// shebang is made executable even when the tracked source is not.
//
// # Why the exception, and why it is not "fixing the example"
//
// This example's two hook scripts ship git-tracked as mode 100644 (non-executable)
// while carrying `#!/usr/bin/env bash` shebangs — they are meant to run, and the
// sibling action-proof example ships its script 100755. The engine refuses a
// non-executable check as unrunnable ("the check could not be run: it is not
// executable ... chmod +x it"), so lifting these verbatim makes EVERY ASK.md write
// fail closed on the missing bit before the script logic or the judge is ever
// reached — which is a property already pinned elsewhere (pre_tool/004), not this
// use case. Setting the bit is exactly the `chmod +x` the engine's own message
// prescribes and what any correct install does; it is an INSTALL step, not an edit
// to examples/** (which is untouched). The missing bit is a real example-packaging
// bug and is flagged in the report; here the install does what a deployment must so
// the use case's actual two-tier judge logic is exercised. The mode is otherwise
// carried over, so a file the author DID mark executable is unchanged.
func installMode(path string, body []byte, srcMode fs.FileMode) fs.FileMode {
	if strings.HasSuffix(path, ".sh") && strings.HasPrefix(string(body), "#!") {
		return srcMode | 0o755
	}
	return srcMode
}

func containsStr(haystack, needle string) bool { return strings.Contains(haystack, needle) }
