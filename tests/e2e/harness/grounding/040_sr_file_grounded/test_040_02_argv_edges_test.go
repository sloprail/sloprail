package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T040_07: a value flag given twice is refused rather than last-wins — another
// reader of the same line could take the first — and nothing is written.
func TestT040_07_RepeatedValueFlagIsRefused(t *testing.T) {
	e, proj, env := session(t, "s-040-07")
	e.WriteFile(proj, "a.md", "keep\n")

	for _, args := range [][]string{
		{"write", "a.md", "--content", "one", "--content", "two"},
		{"edit", "a.md", "--old-string", "keep", "--old_string", "keep", "--new-string", "gone"},
	} {
		res := e.CLIDirectEnv(proj, env, "sr-file", args...)
		if res.Code == 0 || !strings.Contains(res.Output, "given twice") {
			t.Errorf("%v: want a refusal naming the repeated flag, got %d:\n%s", args, res.Code, res.Output)
		}
	}
	if got := read(t, proj, "a.md"); got != "keep\n" {
		t.Errorf("a refused call changed the file: %q", got)
	}

	// --cite: repeats by design, and a pool named twice is one pool.
	res := e.CLIDirectEnv(proj, env, "sr-file", "delete", "a.md",
		"--cite:user,user", "every release", "--cite:user", "keep a CHANGELOG")
	if res.Code != 0 || !strings.Contains(res.Output, "grounded in 2 citations") {
		t.Errorf("two citations were not both grounded, got %d:\n%s", res.Code, res.Output)
	}
}

// T040_08: the `sr file` proxy spelling is the same command, and a path is taken
// verbatim — spaces, a leading dash after `--`, a directory to create.
func TestT040_08_ProxyAndPathSpellings(t *testing.T) {
	e, proj, env := session(t, "s-040-08")

	res := e.CLIDirectEnv(proj, env, "sr", "file", "write", "--cite:user", "every release",
		"--content", "x\n", "--", "dir with space/-odd.md")
	if res.Code != 0 {
		t.Fatalf("proxy write exited %d:\n%s", res.Code, res.Output)
	}
	if got := read(t, proj, "dir with space/-odd.md"); got != "x\n" {
		t.Errorf("content = %q", got)
	}
}

// T040_09: sr-file refuses to change a file through a symbolic link — the change
// would land where the link points, not at the path a rule judged — and the
// link's target is untouched.
// sr:proves citations/file-command-writes-nothing-unless-exact
func TestT040_09_SymbolicLinkIsRefused(t *testing.T) {
	e, proj, env := session(t, "s-040-09")
	e.WriteFile(proj, "memories/decisions.md", "# decisions\n")
	if err := os.Symlink("memories/decisions.md", filepath.Join(proj, "notes.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("memories", filepath.Join(proj, "m")); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"write", "notes.md", "--content", "overwritten"},
		{"edit", "m/decisions.md", "--old-string", "decisions", "--new-string", "gone"},
		{"delete", "m/decisions.md"},
	} {
		res := e.CLIDirectEnv(proj, env, "sr-file", args...)
		if res.Code == 0 || !strings.Contains(res.Output, "symbolic link") {
			t.Errorf("%v: want a refusal naming the link, got %d:\n%s", args, res.Code, res.Output)
		}
	}
	if got := read(t, proj, "memories/decisions.md"); got != "# decisions\n" {
		t.Errorf("the link's target changed: %q", got)
	}
}
