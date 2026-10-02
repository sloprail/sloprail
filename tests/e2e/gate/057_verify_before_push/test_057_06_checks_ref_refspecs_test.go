package e2e

import (
	"strings"
	"testing"
)

// T057_06: checks-ref-sr-only refuses a refspec or config that can WRITE the results ref (by destination), and
// leaves legitimate globs and plain fetch/pull alone.
func TestT057_06_OnlyRefspecsThatCanWriteTheResultsRefAreRefused(t *testing.T) {
	e, proj, _ := spellingsSetup(t)
	e.Run(proj, "s-057-06", "judge", Turns("done",
		Bash("j", "sr-checks run --base "+e.Git(proj, "rev-list", "--max-parents=0", "HEAD")+" --head HEAD")))
	for i, cmd := range []string{
		"git -c 'remote.origin.fetch=+refs/*:refs/*' fetch origin",
		"git -c remote.origin.fetch=+refs/sloprail/*:refs/sloprail/* fetch",
		"git -c 'remote.origin.push=refs/*:refs/*' push origin",
		"git -c remote.origin.mirror=true push origin",
		"git remote add --mirror=fetch m /nonexistent/m.git",
		"git remote add --mirror m /nonexistent/m.git",
		"git config remote.origin.fetch '+refs/*:refs/*'",
		"git fetch origin '+refs/heads/*:refs/sloprail/*'",
		"git fetch origin '+*:*'",
		"git fast-import --force < /dev/null",
		"git config --add remote.origin.fetch '+refs/heads/*:refs/sloprail/*'",
		"git config set remote.origin.fetch '+refs/heads/main:refs/sloprail/checks'",
	} {
		res := e.Run(proj, "s-057-06", "write it", Turns("done", Bash("w"+string(rune('a'+i)), cmd)))
		if !res.Refused() || !(res.Saw("checks-ref-sr-only") || res.Saw("verify-before-push")) {
			t.Fatalf("%q was not refused:\n%s", cmd, res.Output)
		}
	}
	for i, cmd := range []string{
		"git fetch origin 'refs/tags/*:refs/tags/*'",
		"git fetch origin '+refs/heads/*:refs/remotes/origin/*'",
		"git fetch origin '+refs/heads/*:refs/heads/*'",
		"git fetch",
		"git pull",
		"git push origin 'feat-*:feat-*'",
		"git -c remote.origin.fetch='+refs/heads/*:refs/remotes/origin/*' fetch origin",
		"git add '*.md'",
	} {
		res := e.Run(proj, "s-057-06", "ordinary", Turns("done", Bash("o"+string(rune('a'+i)), cmd)))
		if strings.Contains(res.Output, "checks-ref-sr-only") {
			t.Fatalf("%q was refused by the results-branch gate:\n%s", cmd, res.Output)
		}
	}
}
