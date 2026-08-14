package e2e

import (
	"testing"
)

// The other three questions the task settles, each driven through a real
// session so what is proved is the product rather than an arrangement.

// shadowingDecl is a project's OWN rule named `authoring-slop` — the same name
// the plugin ships. It permits, and records that it ran.
//
// It permits deliberately. A project rule that refused would end the dispatch at
// the first refusal, and "the plugin's rule did not fire" would be true whether
// or not precedence worked — the test would pass with the mechanism deleted.
// Permitting means the plugin's rule is the next thing that WOULD run, so its
// silence is evidence.
const shadowingDecl = `---
hooks:
  PreFileCreate:
    - matcher: path endsWith ".sh"
      hooks:
        - type: command
          command: ./mine.sh
---

# The project's own rule of the same name

It permits, so that whether the plugin's rule of this name still fires is
observable rather than masked by an early return.
`

const recordThenPermit = `#!/bin/sh
cat >/dev/null
echo ran >> "$PWD/ledger"
exit 0
`

// T026_03: a project rule of the same name takes precedence over the plugin's.
//
// That the shadowing is also REPORTED is asserted in internal/guardrail rather
// than here; the note below the test says why this stream cannot carry it.
//
// Question 1. The project must be able to override a rule it did not write —
// otherwise installing a plugin imposes rules that can only be escaped by
// uninstalling it, and the first rule anyone disagrees with takes the useful
// ones down with it. But silently is the failure this product exists to prevent:
// a project that displaced a rule and was never told believes it has two
// protections and has one.
func TestT026_03_ProjectRuleTakesPrecedenceOverThePlugins(t *testing.T) {
	e := New(t)
	proj := e.Project()

	// The same NAME the plugin ships, written by the project.
	e.Guardrail(proj, "authoring-slop", shadowingDecl, map[string]string{"mine.sh": recordThenPermit})

	// Content the PLUGIN's rule would refuse. If the plugin's version were still
	// in force, this write would be blocked.
	got := e.Run(proj, "s-026-03", "write a guardrail hook", Turns("done",
		Write("w1", ".sloprail/guardrails/other/check.sh", slopHook),
	))

	// The project's own rule ran. Read from its ledger rather than the stream —
	// it permits, and a permitted hook's stderr reaches no agent. Without this
	// the assertion below could hold because nothing ran at all.
	if runs := e.Ledger(proj, "authoring-slop", "ledger"); len(runs) == 0 {
		t.Fatalf("the project's own rule never ran, so whether it displaced the plugin's "+
			"cannot be concluded:\n%s", got.Output)
	}

	// The plugin's rule of that name did NOT fire.
	if got.Refused() {
		t.Errorf("the plugin's rule refused despite the project declaring one of the same name — "+
			"a project must be able to override a rule it did not write:\n%s", got.Output)
	}
}

// That the shadowing is REPORTED is asserted separately, and not on this stream,
// because of a fact this repo has already measured and written down: beside a
// PERMITTED action neither stdout nor stderr reaches the agent, since permitting
// means exiting 0 and no channel delivers at exit 0 (see the channel table on
// refuseForBroken in services/sr-session). A shadowed rule is precisely the
// permitted case — both declarations are well-formed and the project's is
// enforcing — so there is no agent-facing channel for the warning to travel, and
// escalating it to a refusal would make overriding impossible, which is the
// capability the mechanism exists to provide.
//
// An earlier version of T026_03 asserted `got.Output` contained the warning. It
// failed, correctly: it was asking a channel that cannot carry the text. The
// honest home for the claim is where the report is generated, which is the unit
// test TestResolve_ProjectRuleWinsAndTheShadowingIsReported in
// internal/guardrail — it holds both that the project wins and that the
// displaced rule is named. Restating it here against a stream that measurably
// delivers nothing would be an assertion that cannot fail, which is worse than
// no assertion at all.

// T026_04: a plugin's rule the project switched off is inert.
//
// Question 2. `enabled: false` lives in the declaration, which the consumer does
// not own — and an edit inside the install cache is undone by the next
// reinstall. So the switch-off is made from the project's own side, naming the
// rule `<plugin>/<guardrail>`.
//
// The write here is the one T026_01 proved is refused, so this is that test's
// own control inverted: the same project, the same content, one config file
// different.
func TestT026_04_ADisabledPluginRuleIsInert(t *testing.T) {
	e := New(t)
	proj := e.Project()

	e.DisablePluginGuardrail(proj, "sloprail/authoring-slop")

	got := e.Run(proj, "s-026-04", "write a guardrail hook", Turns("done",
		Write("w1", ".sloprail/guardrails/mine/check.sh", slopHook),
	))

	if got.Refused() {
		t.Fatalf("a plugin rule the project disabled still refused — a consumer who cannot "+
			"switch off a shipped rule can only uninstall the plugin:\n%s", got.Output)
	}
	if !e.Exists(proj, ".sloprail/guardrails/mine/check.sh") {
		t.Errorf("the write did not land even though nothing refused it")
	}
}

// T026_05: disabling is per RULE, not per plugin, and is keyed on the qualified
// name.
//
// A project that disables `sloprail/authoring-slop` must not thereby disable a
// rule of its own that happens to be called `authoring-slop`. Those are
// different rules with different authors, and conflating them would switch off
// a rule the consumer wrote while they were trying to switch off one they
// installed.
func TestT026_05_DisablingAPluginRuleLeavesTheProjectsOwnInForce(t *testing.T) {
	e := New(t)
	proj := e.Project()

	// The project's own rule, named the same, which REFUSES so its being in
	// force is visible on the stream.
	e.Guardrail(proj, "authoring-slop", shadowingDecl, map[string]string{
		"mine.sh": "#!/bin/sh\ncat >/dev/null\necho \"the project's own rule is in force\" >&2\nexit 1\n",
	})
	e.DisablePluginGuardrail(proj, "sloprail/authoring-slop")

	got := e.Run(proj, "s-026-05", "write a guardrail hook", Turns("done",
		Write("w1", ".sloprail/guardrails/other/check.sh", cleanHook),
	))

	if !got.Saw("the project's own rule is in force") {
		t.Fatalf("disabling %q also switched off the PROJECT's own rule of that name — "+
			"the disable list must be keyed on the qualified name:\n%s",
			"sloprail/authoring-slop", got.Output)
	}
}
