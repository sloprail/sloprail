package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T001_07: in GitHub Actions (or with --format github) verify says what is red the way GitHub
// renders it: an ::error annotation per file and rule, a job summary, and the exact local fix.
// Outside Actions the output is unchanged.
func TestT001_07_GitHubNativeResults(t *testing.T) {
	e, proj := session(t)
	base := judged(t, e, proj, verdictPass) // nothing was judged: verify reads "not judged yet"

	plain := checks(e, proj, "verify", "--base", base, "--head", "HEAD")
	if plain.Code != 1 || strings.Contains(plain.Output, "::error") || strings.Contains(plain.Output, "To fix:") {
		t.Fatalf("outside Actions verify must keep its plain output: exit %d:\n%s", plain.Code, plain.Output)
	}

	summary := filepath.Join(t.TempDir(), "summary.md")
	junit := filepath.Join(t.TempDir(), "junit.xml")
	env := append(e.SessionEnv(sessionID), "GITHUB_ACTIONS=true", "GITHUB_STEP_SUMMARY="+summary)
	res := quiet(e.CLIDirectEnv(proj, env, "sr-checks", "verify", "--base", base, "--head", "HEAD", "--junit", junit))
	if res.Code != 1 {
		t.Fatalf("the exit code must stay 1: %d\n%s", res.Code, res.Output)
	}
	contains(t, res.Output, "::error file=docs/a.md,line=1,title=file-guard/docs%3A not judged yet::", "To fix: run `sr-checks run --base "+base+" --head ")
	if n := strings.Count(res.Output, "not judged yet — run"); n != 0 {
		t.Fatalf("the plain refusal text must not repeat the annotated errors (%d):\n%s", n, res.Output)
	}

	contains(t, res.Output, "::stop-commands::")
	if i, j := strings.Index(res.Output, "::stop-commands::"), strings.Index(res.Output, "::error file="); j < 0 || i < 0 {
		t.Fatalf("annotations and guarded text both expected:\n%s", res.Output)
	}

	md, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	contains(t, string(md), "| Rule | Subject | Files | Status | What to do |", "| file-guard/docs | changeset | `docs/a.md` | not judged | `sr-checks run --base "+base, "0 pass, 0 fail, 1 not judged, 0 cached")
	xml, err := os.ReadFile(junit)
	if err != nil {
		t.Fatal(err)
	}
	contains(t, string(xml), `<testsuite name="file-guard/docs"`, `<failure message="not judged">`)

	off := quiet(e.CLIDirectEnv(proj, env, "sr-checks", "verify", "--base", base, "--head", "HEAD", "--format", "plain"))
	if strings.Contains(off.Output, "::error") {
		t.Fatalf("--format plain must turn the annotations off:\n%s", off.Output)
	}
}
