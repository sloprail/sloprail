package e2e

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// quickstartBlock returns the fenced block of docs/getting-started/quickstart.md whose body holds all of marks.
func quickstartBlock(t *testing.T, doc string, marks ...string) string {
	t.Helper()
	for _, m := range regexp.MustCompile("(?s)```[a-z]*\n(.*?)```").FindAllStringSubmatch(doc, -1) {
		ok := true
		for _, mark := range marks {
			ok = ok && strings.Contains(m[1], mark)
		}
		if ok {
			return m[1]
		}
	}
	t.Fatalf("quickstart.md has no fenced block holding %q", marks)
	return ""
}

// T034_09: the rule the quickstart tells a reader to write refuses a bad file and passes a clean one.
// It is built from the quickstart's own text, so the page cannot promise a refusal its script never gives.
func TestT034_09_QuickstartRuleRefusesABadFile(t *testing.T) {
	raw, err := os.ReadFile("../../../../../docs/getting-started/quickstart.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	decl := quickstartBlock(t, doc, "file-guard.yaml", "match:")
	script := quickstartBlock(t, doc, "#!/usr/bin/env bash", "TODO(no-ship)")

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "no-todo-in-committed-code", decl, map[string]string{"check.sh": script})
	base := e.CommitAll(proj, "the rule")

	e.WriteFile(proj, "src/ok.ts", "export const a = 1\n")
	clean := e.CommitAll(proj, "a clean file")
	if r := e.CheckRunRaw(proj, "s-034-09", base, clean); r.Code != 0 {
		t.Fatalf("the clean file was refused (exit %d):\n%s", r.Code, r.Output)
	}

	e.WriteFile(proj, "src/bad.ts", "// TODO(no-ship) fix\nexport const b = 2\n")
	bad := e.CommitAll(proj, "a file with the marker")
	r := e.CheckRunRaw(proj, "s-034-09", base, bad)
	if r.Code == 0 || !strings.Contains(r.Output, "TODO(no-ship)") {
		t.Fatalf("the quickstart rule did not refuse a file holding the marker (exit %d):\n%s", r.Code, r.Output)
	}
}
