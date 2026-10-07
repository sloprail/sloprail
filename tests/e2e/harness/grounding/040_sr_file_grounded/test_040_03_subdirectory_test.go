package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T040_10: a cited sr-file call finds the session's record from a SUBDIRECTORY.
//
// Claude Code files a session's record under the directory the session started
// in, and its Bash tool keeps the working directory between calls — so after an
// earlier `cd internal`, an agent's `sr-file write ... --cite:user` runs from
// below that directory. The record must still be found (by walking up to the
// directory it is filed under), or the real run refuses what the hook's dry run
// — which names the record itself — predicted would succeed.
func TestT040_10_CitesFromASubdirectory(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	const id = "s-040-10"
	e.Run(proj, id, prompt, Turns("done", Bash("b1", "true")))

	sub := filepath.Join(proj, "sub", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(e.SessionEnv(id), "SR_TRANSCRIPT=")
	res := e.CLIDirectEnv(sub, env, "sr-file", "write", "notes.md",
		"--cite:user", "keep a CHANGELOG entry", "--content", "noted\n")
	if res.Code != 0 {
		t.Fatalf("a cited write from a subdirectory exited %d, want 0:\n%s", res.Code, res.Output)
	}
	if got := read(t, proj, "sub/deeper/notes.md"); got != "noted\n" {
		t.Errorf("content = %q", got)
	}
	if !strings.Contains(res.Output, e.TranscriptPath(proj, id)) {
		t.Errorf("the citation did not resolve in the session's own record:\n%s", res.Output)
	}

	// The same walk finds nothing for a session that is not this tree's: a
	// session id with no record above the directory still refuses, and writes
	// nothing.
	env = append(e.SessionEnv("not-this-session"), "SR_TRANSCRIPT=")
	res = e.CLIDirectEnv(sub, env, "sr-file", "write", "other.md",
		"--cite:user", "keep a CHANGELOG entry", "--content", "x\n")
	if res.Code == 0 {
		t.Fatalf("a citation with no session record resolved:\n%s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(sub, "other.md")); !os.IsNotExist(err) {
		t.Errorf("a refused write left a file behind")
	}
}
