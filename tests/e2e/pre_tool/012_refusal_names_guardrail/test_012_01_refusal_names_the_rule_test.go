package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// refusal_names_guardrail: a refusal names the guardrail that produced it.
//
// An agent told only that it was blocked cannot find the rule it broke. Naming
// the guardrail is what makes a refusal actionable rather than merely obstructive,
// and the NEW pre-tool dispatch must carry that name as well.
//
// It used to install OLD-format rules (`hooks: PreFileCreate: [matcher: path
// startsWith "<dir>/"]`) and rely on the old dispatch attaching the guardrail's
// name to the refusal. The new dispatch attributes a gate's refusal itself:
// it returns `"<reason> (gate <attribution>)"`, where a project's own gate's Attribution is its bare quoted
// name (internal/declaration's Origin.Describe). So the
// deny reason carries the guard's name, and these tests re-prove that against the
// new dispatch — with more than one guard declared, so the claim has to
// discriminate the firing rule from the ones that did not.
//
// The old coverage in 004 only asserts a name appears SOMEWHERE; a build naming
// the WRONG rule would satisfy that. Declaring three guards, each bound to its own
// directory so exactly one fires per write, makes "a name was mentioned" and "the
// right name was mentioned" come apart — and only the second is the invariant.

// binding is a NEW-FORMAT gate on PreFileWrite that refuses writes under one
// directory, with a reason that does NOT contain the guard's own name. The name
// must come from the engine's attribution; a check that mentioned itself would let
// a build that attributes nothing pass.
func binding(dir string) string {
	return fmt.Sprintf(`on:
  - event: PreFileWrite
    match: event.path startsWith %q
checks:
  - script: ./refuse.sh
`, dir+"/")
}

const refuseScript = `#!/bin/sh
cat >/dev/null
echo '{"reason":"that is not allowed here"}'
exit 1
`

// names is every guardrail declared in these tests, each bound to its own
// directory so exactly one can fire per write.
var names = map[string]string{
	"no-secrets":   "secrets",
	"no-vendor":    "vendor",
	"no-generated": "generated",
}

// project declares all three guardrails, so every test below has rules that did
// not fire available to be wrongly named.
func project(t *testing.T, e *harness.Env) string {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	for name, dir := range names {
		e.Gate(proj, name, binding(dir), map[string]string{"refuse.sh": refuseScript})
	}
	return proj
}

// T012_01: the refusal names the rule that fired, and not the ones that did not.
//
// Three rules are declared and one is broken. A refusal mentioning any other name
// sends the agent to a rule it did not break — worse than an unnamed refusal,
// because it looks actionable and is wrong.
func TestT012_01_RefusalNamesTheRuleThatFired(t *testing.T) {
	for name, dir := range names {
		t.Run(name, func(t *testing.T) {
			e := New(t)
			proj := project(t, e)

			got := e.Run(proj, "s-012-01-"+name, "write a note", Turns("done",
				Write("w1", dir+"/notes.md", "hello"),
			))

			if !got.Saw("that is not allowed here") {
				t.Fatalf("the write under %q was not refused at all:\n%s", dir, got.Output)
			}
			if !got.Saw(name) {
				t.Fatalf("the refusal never names %q, so the agent cannot find the rule it broke:\n%s", name, got.Output)
			}
			for other := range names {
				if other != name && got.Saw(other) {
					t.Errorf("the refusal names %q, which did not fire — it points at the wrong rule:\n%s", other, got.Output)
				}
			}
		})
	}
}

// T012_02: the name travels with the reason, not merely somewhere in the stream.
//
// The weakness in asserting a name appears anywhere: the agent reads the refusal's
// reason, and a name printed on a separate diagnostic line is not in what it is
// shown. The two have to arrive together to be actionable. The new dispatch builds
// one string, `"<reason> (gate \"no-secrets\")"`, so the name rides on the
// same line the reason does.
func TestT012_02_TheNameIsInTheReasonTheAgentReads(t *testing.T) {
	e := New(t)
	proj := project(t, e)

	got := e.Run(proj, "s-012-02", "write a note", Turns("done",
		Write("w1", "secrets/notes.md", "hello"),
	))

	// The line carrying what the check said is the line the agent acts on. The
	// guardrail's name has to be on it.
	var carrying []string
	for _, line := range strings.Split(got.Output, "\n") {
		if strings.Contains(line, "that is not allowed here") {
			carrying = append(carrying, line)
		}
	}
	if len(carrying) == 0 {
		t.Fatalf("the refusal never reached the agent:\n%s", got.Output)
	}
	for _, line := range carrying {
		if !strings.Contains(line, "no-secrets") {
			t.Fatalf("the reason the agent is shown does not name the guardrail:\n%s", line)
		}
	}
}

// T012_03: a refusal the check gave no words for still names its guardrail.
//
// The case with the least to work with, and the one where naming matters most:
// there is nothing quoted back to recognise the rule by, so the name is the only
// thing that makes the refusal traceable. The check exits non-zero with no reason;
// the engine supplies its own text and still attributes the guard.
func TestT012_03_SilentRefusalStillNamesItsGuardrail(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "no-secrets", binding("secrets"), map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\nexit 1\n",
	})

	got := e.Run(proj, "s-012-03", "write a note", Turns("done",
		Write("w1", "secrets/notes.md", "hello"),
	))

	if !got.Refused() {
		t.Fatalf("a silent non-zero check did not refuse the write:\n%s", got.Output)
	}
	if !got.Saw("no-secrets") {
		t.Fatalf("a refusal with no reason of its own did not name the rule that produced it:\n%s", got.Output)
	}
}
