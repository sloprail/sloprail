package e2e

import (
	"strings"
	"testing"
)

// T058_32: a Stop refusing one range for two different rules prints the header once, the range as one
// item and its rules as sub-items; it is not nested under a second header of the range's own.
func TestT058_32_StopHeaderIsPrintedOnce(t *testing.T) {
	e, proj := merging(t, true)
	e.FileGuard(proj, "strict-docs", "match: \"docs/**\"\nchecks:\n  - script: ./no.sh\n",
		map[string]string{"no.sh": "#!/bin/sh\necho '{\"reason\":\"strict-docs objects to the docs\"}'\nexit 1\n"})
	e.CommitAll(proj, "a second rule", "Sloprail-Cites-User: "+quote)
	e.Run(proj, "s-058-32", prompt, Turns("done",
		Bash("m", "git merge -q --no-commit side || true"),
		Bash("r", "printf 'resolved\\n' > docs/seed.md && git add docs/seed.md"),
		Bash("c", "GIT_EDITOR=true git merge --continue"),
	))
	conts := e.StopContinuations(proj, "s-058-32")
	if len(conts) == 0 {
		t.Fatal("the Stop did not refuse")
	}
	got := conts[0]
	has(t, got, "cited-docs")
	has(t, got, "strict-docs objects to the docs")
	if n := strings.Count(got, "rules refused"); n != 1 {
		t.Fatalf("the Stop's header is printed %d times, want once:\n%s", n, got)
	}
	has(t, got, "the following rules refused this turn's work:\n  - In ")
	has(t, got, "\n    - ")
}
