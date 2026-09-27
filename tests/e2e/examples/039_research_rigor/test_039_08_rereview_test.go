package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Holes a second review found in the fixes of test_039_07.

// T039_33: a clone is confirmed by git's own record of it, not by output the
// agent controls. Each case ends with two source reads of a directory that is
// not a clone this run made, and each was admitted before: output forged to
// look like git's, a hand-made .git with no repository behind it, and a stale
// .git copied to refresh its file times.
func TestT039_33_CloneEvidenceTheAgentControlsIsNotCredited(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, src, d string) string
	}{
		{"git's announcement echoed over a hidden failure", func(t *testing.T, src, d string) string {
			staleClone(t, src, d)
			return "git clone " + src + " " + d + " 2>/dev/null; echo \"Cloning into '" + d + "'...\""
		}},
		{"a hand-made .git and a clone that cannot run", func(t *testing.T, _, d string) string {
			return "mkdir -p " + d + "/.git " + d + "/lib && echo 'a()' > " + d + "/lib/retry.js && echo 'b()' > " + d + "/lib/backoff.js" +
				" && git clone -q https://invalid.invalid/x.git " + d + " 2>/dev/null; true"
		}},
		{"a stale .git copied to look new", func(t *testing.T, src, d string) string {
			staleClone(t, src, d)
			return "git clone -q " + src + " " + d + " 2>/dev/null; mv " + d + "/.git " + d + "/.g && cp -R " + d + "/.g " + d + "/.git"
		}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, proj := research(t)
			src := sourceRepo(t, e, "retry-lib")
			d := filepath.Join(scratch(t), "retry-lib")
			joined := refused(t, e, proj, "s-039-33-"+string(rune('a'+i)),
				SayBash("b1", "#research", tc.setup(t, src, d)),
				Read("r1", filepath.Join(d, "lib", "retry.js")),
				Read("r2", filepath.Join(d, "lib", "backoff.js")),
			)
			for _, want := range []string{noCloneReason, "Your git clone into " + d + " could not be confirmed"} {
				if !strings.Contains(joined, want) {
					t.Errorf("the refusal is missing %q:\n%s", want, joined)
				}
			}
		})
	}
}

// T039_34: reads that show no source content do not count — a search that
// printed nothing in the words real Claude Code uses for it, a count, a file
// list, and the Grep tool in its default (file names only) mode — and neither
// does project metadata, nor the same file reached through a second spelling
// (a symlinked directory), nor a file a symlink leads to OUT of the clone.
func TestT039_34_ReadsThatShowNoSourceDoNotCount(t *testing.T) {
	type build func(t *testing.T, dst string) []harness.Turn
	cases := []struct {
		name  string
		turns build
	}{
		{"no output, as Claude Code records it", func(t *testing.T, dst string) []harness.Turn {
			cmd := "grep -rn zzz-nomatch " + filepath.Join(dst, "lib") + " " + filepath.Join(dst, "index.js") + " | head -20"
			use, res := harness.CallWithOutput("g1", "Bash", map[string]string{"command": cmd}, "(Bash completed with no output)")
			return []harness.Turn{Read("r1", filepath.Join(dst, "lib", "retry.js")), use, res}
		}},
		{"a count", func(t *testing.T, dst string) []harness.Turn {
			return []harness.Turn{
				Bash("b2", "grep -c zzz "+filepath.Join(dst, "lib", "backoff.js")+" "+filepath.Join(dst, "index.js")+"; true"),
				Read("r1", filepath.Join(dst, "lib", "retry.js")),
			}
		}},
		{"a file list", func(t *testing.T, dst string) []harness.Turn {
			return []harness.Turn{
				Bash("b2", "grep -rl require "+filepath.Join(dst, "lib")+" "+filepath.Join(dst, "index.js")),
				Read("r1", filepath.Join(dst, "lib", "retry.js")),
			}
		}},
		{"the Grep tool listing file names", func(t *testing.T, dst string) []harness.Turn {
			use, res := harness.CallWithOutput("g1", "Grep",
				map[string]string{"pattern": "require", "path": filepath.Join(dst, "index.js")},
				"Found 1 file\n"+filepath.Join(dst, "index.js"))
			return []harness.Turn{Read("r1", filepath.Join(dst, "lib", "retry.js")), use, res}
		}},
		{"project metadata", func(t *testing.T, dst string) []harness.Turn {
			return []harness.Turn{
				Read("r1", filepath.Join(dst, "package.json")),
				Read("r2", filepath.Join(dst, "lib", "retry.js")),
			}
		}},
		{"one file through a symlinked directory", func(t *testing.T, dst string) []harness.Turn {
			return []harness.Turn{
				Bash("b2", "ln -s lib "+filepath.Join(dst, "lib2")),
				Read("r1", filepath.Join(dst, "lib", "retry.js")),
				Read("r2", filepath.Join(dst, "lib2", "retry.js")),
			}
		}},
		{"a symlink out of the clone", func(t *testing.T, dst string) []harness.Turn {
			outside := filepath.Join(scratch(t), "elsewhere")
			if err := os.MkdirAll(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(outside, "other.js"), []byte("x()\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return []harness.Turn{
				Bash("b2", "ln -s "+outside+" "+filepath.Join(dst, "ext")),
				Read("r1", filepath.Join(dst, "lib", "retry.js")),
				Read("r2", filepath.Join(dst, "ext", "other.js")),
			}
		}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, proj := research(t)
			src := sourceRepo(t, e, "retry-lib")
			dst := filepath.Join(scratch(t), "retry-lib")
			turns := append([]harness.Turn{SayBash("b1", "#research", "git clone "+src+" "+dst)}, tc.turns(t, dst)...)
			joined := refused(t, e, proj, "s-039-34-"+string(rune('a'+i)), turns...)
			want := "This #research run cloned " + dst + " but read only one source file in it (" + filepath.Join(dst, "lib", "retry.js")
			if !strings.Contains(joined, want) {
				t.Errorf("the refusal is missing %q:\n%s", want, joined)
			}
		})
	}
}

// T039_35: the notes stay held however the write reaches them while research
// is open: an eval of an unreadable payload before an ordinary append (the
// directory is not known, but the write still is), an eval whose payload IS
// the write, a copy by rsync, and a write through a symbolic or hard link made
// in an earlier call. Each left NOTES.md rewritten before.
func TestT039_35_NotesWritesThatRouteAroundTheMatchAreHeld(t *testing.T) {
	cases := []struct {
		name  string
		turns []harness.Turn
	}{
		{"after an eval of an unreadable payload", []harness.Turn{
			Bash("b2", `eval "$(true)"; echo '## Proposed approach' >> NOTES.md`),
		}},
		{"inside a literal eval", []harness.Turn{
			Bash("b2", `eval 'echo "## Proposed approach" >> NOTES.md'`),
		}},
		{"copied in by rsync", []harness.Turn{
			Bash("b2", "printf '## Proposed approach\\n' > draft.txt"),
			Bash("b3", "rsync draft.txt NOTES.md"),
		}},
		{"through a symbolic link", []harness.Turn{
			Bash("b2", "ln -s NOTES.md n.txt"),
			Bash("b3", "echo '## Proposed approach' >> n.txt"),
		}},
		{"through a hard link", []harness.Turn{
			Bash("b2", "ln NOTES.md n.txt"),
			Bash("b3", "echo '## Proposed approach' >> n.txt"),
		}},
		{"through a link made on the same line", []harness.Turn{
			Bash("b2", "ln -s NOTES.md n.txt && echo '## Proposed approach' >> n.txt"),
		}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, proj := notesProject(t)
			turns := append([]harness.Turn{SayBash("m1", "Researching retry libraries. #research", "echo start")}, tc.turns...)
			res := e.Run(proj, "s-039-35-"+string(rune('a'+i)), "research retry", Turns("done", turns...))
			if got := notes(t, proj); got != seedNotes {
				t.Fatalf("the proposal reached NOTES.md:\n%s\n%s", got, res.Output)
			}
			if !res.Saw("now would record this #research run's findings") {
				t.Errorf("the refusal did not name the held write:\n%s", res.Output)
			}
		})
	}
}
