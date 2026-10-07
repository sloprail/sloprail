package e2e

import (
	"strings"
	"testing"
)

// T057_14: a lost word where ANY global option's value stands may split into more words at run time
// (`-c $A push` with A='x=y -C /other'), so the push is not judged against the hook's cwd; and a
// quoted `export "GIT_DIR=..."` is still a redirection.
func TestT057_14_LostOptionValueAndQuotedExportFailClosed(t *testing.T) {
	for i, cmd := range []string{
		"git -c core.abbrev=$A push -q origin HEAD:refs/heads/work",
		"git -c a=$A $B push -q origin HEAD:refs/heads/work",
		"export \"GIT_DIR=%s/.git\"; git push -q origin HEAD:refs/heads/work",
	} {
		e, proj, _ := project(t, docsRule)
		c := strings.ReplaceAll(cmd, "%s", proj)
		res := e.Run(proj, "s-057-14-"+string(rune('a'+i)), "push", Turns("done", Bash("p", c)))
		if !res.Refused() {
			t.Fatalf("%q was not refused:\n%s", c, res.Output)
		}
	}
}
