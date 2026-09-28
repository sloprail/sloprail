package repo

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A guard script that sources a helper (`. "$lib"`) checks the helper actually
// loaded. bash stops a sourced file at a syntax error without failing the `.`,
// so a partly-loaded helper defines nothing, and a script that reads on can
// decide on nothing — for a `when`, exit 1 waives the requirement.

var sourceLine = regexp.MustCompile(`^\s*\.\s+"\$(\{[A-Za-z_]+[^}]*\}|[A-Za-z_]+)[^"]*"`)

// Every shipped guard script that sources a helper checks, within the next
// few lines, that a function it needs is defined.
func TestSourcedHelpersAreCheckedLoaded(t *testing.T) {
	root := repoRoot(t)
	checked := 0
	for _, f := range trackedBashScripts(t, root) {
		if !strings.Contains(f, "/.sloprail/") {
			continue // eval scorers and tools source a shared harness, not a guard helper
		}
		lines := readLines(t, filepath.Join(root, f))
		for i, l := range lines {
			if !sourceLine.MatchString(l) {
				continue
			}
			checked++
			window := strings.Join(lines[i+1:min(len(lines), i+5)], "\n")
			if !strings.Contains(window, "declare -F") {
				t.Errorf("%s:%d sources a helper without checking it loaded (declare -F …) — a partly-loaded helper would decide nothing", f, i+1)
			}
		}
	}
	if checked < 8 {
		t.Fatalf("found only %d helper-sourcing lines — the scan is wrong", checked)
	}
}

// The two `when` scripts, where exit 1 waives the requirement, APPLY it (exit
// 0) when their helper does not load — on a payload that, read with a working
// helper, they would waive.
func TestWhenScriptsApplyWhenTheirHelperDoesNotLoad(t *testing.T) {
	root := repoRoot(t)
	for _, tc := range []struct{ guard, script, helper, payload string }{
		{
			"marketplace/plugins/sloprail-tasks/.sloprail/file-guard/task-body-is-human-authored",
			"body-changed.sh", "lib-body.sh",
			`{"event":{"kind":"PreFileUpdate","path":"memories/tasks/a/b/TASK.md","resultKnown":true,"oldContent":"---\nstatus: open\n---\nsame body\n","newContent":"---\nstatus: open\n---\nsame body\n"}}`,
		},
		{
			"marketplace/plugins/sloprail-content/.sloprail/file-guard/unit-publish-approved",
			"enters-published.sh", "publish-claim.sh",
			`{"event":{"kind":"PreFileUpdate","path":"memories/topics/t/units/01/UNIT.md","resultKnown":true,"oldContent":"---\nstatus: drafting\n---\n","newContent":"---\nstatus: drafting\n---\nedited\n"}}`,
		},
	} {
		t.Run(tc.script, func(t *testing.T) {
			dir := t.TempDir()
			src, err := os.ReadFile(filepath.Join(root, tc.guard, tc.script))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, tc.script), src, 0o755); err != nil {
				t.Fatal(err)
			}
			// The zsh-only construct that shipped once: a bash syntax error.
			if err := os.WriteFile(filepath.Join(dir, tc.helper), []byte("x=\"${y:-it's}\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", filepath.Join(dir, tc.script))
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "SR_GUARDRAIL_DIR="+dir)
			cmd.Stdin = strings.NewReader(tc.payload)
			out, err := cmd.CombinedOutput()
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
			if code != 0 {
				t.Errorf("%s with a helper that did not load exited %d (1 waives the requirement), want 0 (apply):\n%s", tc.script, code, out)
			}
		})
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}
