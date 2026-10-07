package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

const prompt = "please keep a CHANGELOG entry for every release"

// session drives one mock session whose prompt is the citable user message and
// returns the project and the env that points sr-file at that transcript.
func session(t *testing.T, id string) (*harness.Env, string, []string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Run(proj, id, prompt, Turns("done", Bash("b1", "true")))
	return e, proj, []string{"SR_TRANSCRIPT=" + e.TranscriptPath(proj, id)}
}

func read(t *testing.T, proj, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(proj, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// T040_01: write creates a file from --content, grounded in a resolving quote,
// and reports the citation's location.
func TestT040_01_WriteWithCitation(t *testing.T) {
	e, proj, env := session(t, "s-040-01")

	res := e.CLIDirectEnv(proj, env, "sr-file", "write", "CHANGELOG.md",
		"--cite:user", "keep a CHANGELOG entry", "--content", "# Changelog\n")
	if res.Code != 0 {
		t.Fatalf("write exited %d:\n%s", res.Code, res.Output)
	}
	if got := read(t, proj, "CHANGELOG.md"); got != "# Changelog\n" {
		t.Errorf("content = %q", got)
	}
	if !strings.Contains(res.Output, "grounded in 1 citation") || !strings.Contains(res.Output, ".jsonl:") {
		t.Errorf("output does not report the resolved citation:\n%s", res.Output)
	}
}

// T040_02: write reads the body from stdin when --content is absent.
func TestT040_02_WriteFromStdin(t *testing.T) {
	e, proj, env := session(t, "s-040-02")

	res := e.CLIDirectStdinEnv(proj, "from stdin\n", env, "sr-file", "write", "notes/a.md")
	if res.Code != 0 {
		t.Fatalf("write exited %d:\n%s", res.Code, res.Output)
	}
	if got := read(t, proj, "notes/a.md"); got != "from stdin\n" {
		t.Errorf("content = %q, want the stdin body (parent dir created)", got)
	}
}

// T040_03: edit has Edit's semantics — a unique match is replaced; an ambiguous
// one is refused unless --replace-all; a missing one is refused.
func TestT040_03_EditSemantics(t *testing.T) {
	e, proj, env := session(t, "s-040-03")
	e.WriteFile(proj, "a.md", "one two two\n")

	if res := e.CLIDirectEnv(proj, env, "sr-file", "edit", "a.md", "--old-string", "one", "--new-string", "ONE"); res.Code != 0 {
		t.Fatalf("unique edit exited %d:\n%s", res.Code, res.Output)
	}
	if res := e.CLIDirectEnv(proj, env, "sr-file", "edit", "a.md", "--old-string", "two", "--new-string", "2"); res.Code == 0 || !strings.Contains(res.Output, "occurs 2 times") {
		t.Errorf("an ambiguous --old-string must be refused, got %d:\n%s", res.Code, res.Output)
	}
	if res := e.CLIDirectEnv(proj, env, "sr-file", "edit", "a.md", "--old-string", "two", "--new-string", "2", "--replace-all"); res.Code != 0 {
		t.Fatalf("--replace-all edit exited %d:\n%s", res.Code, res.Output)
	}
	if got := read(t, proj, "a.md"); got != "ONE 2 2\n" {
		t.Errorf("content = %q, want %q", got, "ONE 2 2\n")
	}
	if res := e.CLIDirectEnv(proj, env, "sr-file", "edit", "a.md", "--old-string", "absent", "--new-string", "x"); res.Code == 0 {
		t.Errorf("a missing --old-string must be refused:\n%s", res.Output)
	}
}

// T040_04: a citation that does not resolve fails the command and writes
// NOTHING — not even a file that the rest of the arguments describe correctly.
// sr:proves citations/file-command-writes-nothing-unless-exact
func TestT040_04_UnresolvedCitationWritesNothing(t *testing.T) {
	e, proj, env := session(t, "s-040-04")
	e.WriteFile(proj, "a.md", "keep\n")

	res := e.CLIDirectEnv(proj, env, "sr-file", "edit", "a.md",
		"--old-string", "keep", "--new-string", "gone", "--cite:user", "the user never said this")
	if res.Code == 0 {
		t.Fatalf("an unresolved citation was accepted:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "nothing written") {
		t.Errorf("the refusal does not say nothing was written:\n%s", res.Output)
	}
	if got := read(t, proj, "a.md"); got != "keep\n" {
		t.Errorf("the file changed despite the refusal: %q", got)
	}

	res = e.CLIDirectEnv(proj, env, "sr-file", "delete", "a.md", "--cite:tool_result", "keep a CHANGELOG")
	if res.Code == 0 {
		t.Errorf("a user quote cited as a tool_result must not resolve:\n%s", res.Output)
	}
	if !e.Exists(proj, "a.md") {
		t.Errorf("the file was deleted despite the refusal")
	}
}

// T040_05: delete removes an existing file and refuses a missing one.
func TestT040_05_Delete(t *testing.T) {
	e, proj, env := session(t, "s-040-05")
	e.WriteFile(proj, "a.md", "x\n")

	if res := e.CLIDirectEnv(proj, env, "sr-file", "delete", "a.md", "--cite:user", "every release"); res.Code != 0 {
		t.Fatalf("delete exited %d:\n%s", res.Code, res.Output)
	}
	if e.Exists(proj, "a.md") {
		t.Errorf("a.md still exists")
	}
	if res := e.CLIDirectEnv(proj, env, "sr-file", "delete", "a.md"); res.Code == 0 {
		t.Errorf("deleting a missing file must fail:\n%s", res.Output)
	}
}

// T040_06: resolve mode — the file is untouched and the change, with its
// resolved citation, is recorded into the named directory, never stdout.
func TestT040_06_ResolveModeRecordsInsteadOfWriting(t *testing.T) {
	e, proj, env := session(t, "s-040-06")
	e.WriteFile(proj, "a.md", "old body\n")
	dir := t.TempDir()
	env = append(env, "SR_FILE_RESOLVE_DIR="+dir)

	res := e.CLIDirectEnv(proj, env, "sr-file", "edit", "a.md",
		"--old-string", "old", "--new-string", "new", "--cite:user", "every release")
	if res.Code != 0 {
		t.Fatalf("resolve-mode edit exited %d:\n%s", res.Code, res.Output)
	}
	if got := read(t, proj, "a.md"); got != "old body\n" {
		t.Errorf("resolve mode changed the file: %q", got)
	}
	if strings.TrimSpace(res.Output) != "" {
		t.Errorf("resolve mode must not use stdout, got:\n%s", res.Output)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "resolved.jsonl"))
	if err != nil {
		t.Fatalf("no record written: %v", err)
	}
	var rec struct {
		Verb       string `json:"verb"`
		Path       string `json:"path"`
		Existed    bool   `json:"existed"`
		OldContent string `json:"oldContent"`
		NewContent string `json:"newContent"`
		Citations  []struct {
			Quote       string   `json:"quote"`
			SourceTypes []string `json:"sourceTypes"`
			Line        int      `json:"line"`
		} `json:"citations"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &rec); err != nil {
		t.Fatalf("record is not one JSON object: %v\n%s", err, raw)
	}
	if rec.Verb != "edit" || !rec.Existed || rec.OldContent != "old body\n" || rec.NewContent != "new body\n" {
		t.Errorf("record = %+v", rec)
	}
	if !strings.HasSuffix(rec.Path, "/a.md") || !filepath.IsAbs(rec.Path) {
		t.Errorf("path %q is not the absolute target", rec.Path)
	}
	if len(rec.Citations) != 1 || rec.Citations[0].Quote != "every release" || rec.Citations[0].Line == 0 ||
		len(rec.Citations[0].SourceTypes) != 1 || rec.Citations[0].SourceTypes[0] != "user" {
		t.Errorf("citations = %+v", rec.Citations)
	}

	// A second invocation in the same resolve directory sees the first's result.
	res = e.CLIDirectEnv(proj, env, "sr-file", "edit", "a.md", "--old-string", "new", "--new-string", "newer")
	if res.Code != 0 {
		t.Fatalf("a chained resolve read disk instead of the overlay:\n%s", res.Output)
	}
}
