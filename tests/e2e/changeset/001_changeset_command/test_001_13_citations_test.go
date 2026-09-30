package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"strings"
	"testing"
)

// T001_13: `Sloprail-Cites-User:` trailers in the range become
// changeset.citations, resolved against the session's transcript like
// `sr-file --cite:user` — and a quote that resolves nowhere, or that is only in
// the wrong pool, is not a citation.
func TestT001_13_TrailerCitationsResolveAgainstTheTranscript(t *testing.T) {
	const prompt = "please split the oversized runner files by move-not-rewrite"
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/a.md", "one\n")
	e.CommitAll(proj, "before")
	e.FileGuard(proj, "size", docsRule(""), map[string]string{"check.sh": passingCheck})
	e.CommitAll(proj, "add the rule")
	e.Run(proj, "s-001-13", prompt, Turns("done", Bash("b1", "true")))
	env := e.SessionEnv("s-001-13")

	// Refusal first: words nobody said, and the user's words claimed as tool
	// output, ground nothing.
	e.WriteFile(proj, "docs/a.md", "one\ntwo\n")
	e.CommitAll(proj, "ungrounded", CitesUser("delete everything please"), CitesTool("oversized runner files"))
	got, res := show(t, e, proj, env, "size")
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	if n := len(got.Payload.Changeset.Citations); n != 0 {
		t.Fatalf("citations = %+v, want none", got.Payload.Changeset.Citations)
	}
	if len(got.UnresolvedCitations) != 2 {
		t.Fatalf("unresolved = %v, want both trailers named", got.UnresolvedCitations)
	}

	// Then a real quote, in a later commit of the same range: the citations
	// accumulate over the range.
	e.WriteFile(proj, "docs/a.md", "one\ntwo\nthree\n")
	e.CommitAll(proj, "grounded", CitesUser("oversized runner files"))
	got, res = show(t, e, proj, env, "size")
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	cites := got.Payload.Changeset.Citations
	if len(cites) != 1 || cites[0].Quote != "oversized runner files" ||
		len(cites[0].SourceTypes) != 1 || cites[0].SourceTypes[0] != "user" ||
		!strings.Contains(cites[0].Message, prompt) {
		t.Fatalf("citations = %+v, want the user's message cited", cites)
	}
}

// T001_14: outside any session there is no transcript to ground a quote in, so
// nothing is resolved — and the command says so rather than inventing citations.
func TestT001_14_NoSessionNoCitations(t *testing.T) {
	e, proj, _ := repoWithRule(t, docsRule(""))
	e.WriteFile(proj, "docs/a.md", "one\ntwo\n")
	e.CommitAll(proj, "claims a quote", CitesUser("anything"))

	got, res := show(t, e, proj, harness.NoSessionEnv, "size")
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	if len(got.Payload.Changeset.Citations) != 0 {
		t.Fatalf("citations = %+v with no session to ground them", got.Payload.Changeset.Citations)
	}
	if v := got.Payload.Changeset.Commits[0].Trailers["Sloprail-Cites-User"]; len(v) != 1 {
		t.Fatalf("the trailer itself is still carried on the commit: %+v", got.Payload.Changeset.Commits[0])
	}
}
