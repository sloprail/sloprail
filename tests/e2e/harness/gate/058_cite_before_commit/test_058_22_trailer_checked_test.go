package e2e

import (
	"strings"
	"testing"
)

// T058_22: the refusal says the quote rides on the commit as a trailer and the gate checks it; it no
// longer tells the agent to run a cite alone first.
func TestT058_22_RefusalAsksForATrailerNotAChainedCite(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-22", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("c", "git commit -q -m 'add a'"),
	))
	if !res.Refused() {
		t.Fatalf("an uncited commit was not refused:\n%s", res.Output)
	}
	has(t, res.Output, "as a trailer")
	has(t, res.Output, "the rule checks it")
	refusal := strings.Join(res.Refusals(), "\n")
	for _, gone := range []string{"cite part alone", "trajectory cite"} {
		if strings.Contains(refusal, gone) {
			t.Fatalf("the refusal still mentions %q:\n%s", gone, refusal)
		}
	}
}

// T058_23: a cite run alone (it resolves), then a commit with -F msg carrying the same trailer, the
// message file beside the repository: the gate resolves the trailer itself and lets it through.
func TestT058_23_CiteAloneThenCommitWithMessageFilePasses(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-23", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("q", "sr-session trajectory cite '"+quote+"'"),
		Bash("m", "printf 'add a\\n\\nSloprail-Cites-User: "+quote+"\\n' > ../msg"),
		Bash("c", "git commit -q -F ../msg"),
	))
	if res.Refused() {
		t.Fatalf("a commit whose -F message carries a resolving trailer was refused after a lone cite:\n%s", res.Output)
	}
	has(t, subjects(e, proj), "add a")
}

// T058_24: a tool-output trailer (Sloprail-Cites-Tool) in a -F message resolves in the same way.
func TestT058_24_ToolTrailerInMessageFilePasses(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-24", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("t", "echo unique-tool-output-marker"),
		Bash("m", "printf 'add a\\n\\nSloprail-Cites-Tool: unique-tool-output-marker\\n' > ../msg"),
		Bash("c", "git commit -q -F ../msg"),
	))
	if res.Refused() {
		t.Fatalf("a commit whose -F message carries a resolving tool trailer was refused:\n%s", res.Output)
	}
	has(t, subjects(e, proj), "add a")
}

// T058_25: a quote that resolves in more than one place is ambiguous: refused, saying so.
func TestT058_25_AmbiguousTrailerIsRefused(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-25", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("t1", "echo dup-marker-one"),
		Bash("t2", "echo dup-marker-one"),
		Bash("c", "git commit -q -m 'add a' -m 'Sloprail-Cites-Tool: dup-marker-one'"),
	))
	if !res.Refused() {
		t.Fatalf("a commit with an ambiguous trailer was not refused:\n%s", res.Output)
	}
	has(t, res.Output, "did not resolve")
}
