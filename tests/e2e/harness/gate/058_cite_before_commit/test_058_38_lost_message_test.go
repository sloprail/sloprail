package e2e

import "testing"

// T058_38: a message the engine cannot resolve does not swallow the option after it: `-m "$(...)" -a`
// still commits every tracked change, and a guarded one still needs its cite.
func TestT058_38_UnresolvableMessageDoesNotHideTheNextOption(t *testing.T) {
	e, proj := project(t)
	oth := other(t, e)
	e.WriteFile(oth, "docs/seed.md", "changed\n")
	res := e.Run(proj, "s-058-38", prompt, Turns("done",
		Bash("c", "git -C "+oth+" commit -q -m \"$(printf x)\" -a"),
	))
	if !res.Refused() {
		t.Fatalf("an uncited commit -a of a guarded file was not refused:\n%s", res.Output)
	}
	has(t, res.Output, "docs/seed.md")
}
