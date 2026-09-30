package e2e

import (
	"testing"
)

// The other invariants, each driven through a real session so what is proved is
// the product rather than an arrangement. All three now target the MIGRATED
// new-format authoring-slop, so precedence and disabling are keyed the new
// format's way: on (nature, name), and on the qualified name
// `<plugin>/<nature>/<name>`.

// shadowingGuardYAML is a project's OWN new-format gate named `authoring-slop` —
// the same (nature, name) the plugin's pre-write half ships. It matches the same
// new-format guardrail scripts, and its check permits while recording that it ran.
//
// Precedence is keyed on (nature, name): a project declaration of the same nature
// and name SHADOWS the plugin's, so the plugin's authoring-slop is dropped and
// only this one runs. It permits deliberately — the point of the test is that the
// PLUGIN's rule (which would refuse the slop hook) does not get a say, and a
// permitted write that lands is the visible proof of that.
const shadowingGuardYAML = `on:
  - event: PreFileWrite
    match: (event.path contains ".sloprail/file-guard/" or event.path contains ".sloprail/gate/" or event.path contains ".sloprail/context/") and event.path endsWith ".sh"
checks:
  - script: ./check.sh
`

const recordThenPermit = `#!/bin/sh
cat >/dev/null
echo ran >> "$SR_GUARDRAIL_DIR/ledger"
exit 0
`

// T026_03: a project rule of the same (nature, name) takes precedence over the
// plugin's.
//
// The project must be able to override a rule it did not write — otherwise
// installing a plugin imposes rules that can only be escaped by uninstalling it,
// and the first rule anyone disagrees with takes the useful ones down with it.
// That the shadowing is also REPORTED is asserted in internal/declaration rather
// than on this stream; the note below the test says why this stream cannot carry
// it.
func TestT026_03_ProjectRuleTakesPrecedenceOverThePlugins(t *testing.T) {
	e := New(t)
	proj := e.Project()

	// The same (nature, name) the plugin ships — a gate named
	// authoring-slop — written by the project.
	e.Gate(proj, "authoring-slop", shadowingGuardYAML, map[string]string{"check.sh": recordThenPermit})

	// Content the PLUGIN's rule would refuse (the tool-name allowlist). If the
	// plugin's version were still in force, this write would be blocked. Reads
	// the skill first — the shipped read-script-checks-doc guard is a DIFFERENT
	// (nature, name) from authoring-slop, so shadowing that one does not touch
	// this precondition.
	got := e.Run(proj, "s-026-03", "write a guardrail hook", Turns("done",
		readSkillFirst(t, Write("w1", ".sloprail/gate/other/check.sh", slopHook))...,
	))

	// The project's own rule ran. Read from its ledger rather than the stream — it
	// permits, and a permitted hook's stderr reaches no agent. Without this the
	// assertion below could hold because nothing ran at all.
	if runs := len(e.GateLedgerLines(proj, "authoring-slop", "ledger")); runs == 0 {
		t.Fatalf("the project's own rule never ran, so whether it displaced the plugin's "+
			"cannot be concluded:\n%s", got.Output)
	}

	// The plugin's rule of that (nature, name) did NOT fire — it was shadowed.
	if got.Refused() {
		t.Errorf("the plugin's rule refused despite the project declaring one of the same "+
			"(nature, name) — a project must be able to override a rule it did not write:\n%s", got.Output)
	}
	// And the write the project's rule permitted actually landed.
	if !e.Exists(proj, ".sloprail/gate/other/check.sh") {
		t.Errorf("the permitted write did not land")
	}
}

// That the shadowing is REPORTED is asserted separately, and not on this stream,
// because of a fact this repo has already measured and written down: beside a
// PERMITTED action neither stdout nor stderr reaches the agent, since permitting
// means exiting 0 and no channel delivers at exit 0. A shadowed rule is precisely
// the permitted case — both declarations are well-formed and the project's is
// enforcing — so there is no agent-facing channel for the warning to travel, and
// escalating it to a refusal would make overriding impossible, which is the
// capability the mechanism exists to provide. The honest home for the claim is
// internal/declaration, where the store computes res.Shadowed and names the
// displaced plugin rule.

// T026_04: a plugin's rule the project switched off is inert.
//
// `enabled: false` lives in the declaration, which the consumer does not own —
// and an edit inside the install cache is undone by the next reinstall. So the
// switch-off is made from the project's own side, naming the rule by its
// qualified name `<plugin>/<nature>/<name>` — for the migrated new-format pre-write
// gate, `sloprail/gate/authoring-slop` (and its Stop-time file-guard half,
// `sloprail/file-guard/authoring-slop`).
//
// The write here is the one T026_01 proved is refused, so this is that test's own
// control inverted: the same project, the same content, one config file
// different.
func TestT026_04_ADisabledPluginRuleIsInert(t *testing.T) {
	e := New(t)
	proj := e.Project()

	e.DisablePluginGuardrail(proj, "sloprail/gate/authoring-slop")
	e.DisablePluginGuardrail(proj, "sloprail/file-guard/authoring-slop")

	// Reads the skill first — disabling authoring-slop is a DIFFERENT (nature,
	// name) from the shipped read-script-checks-doc guard, so this write must
	// still clear that one on its own.
	got := e.Run(proj, "s-026-04", "write a guardrail hook", Turns("done",
		readSkillFirst(t, Write("w1", newFormatGuardHook, slopHook))...,
	))

	if got.Refused() {
		t.Fatalf("a plugin rule the project disabled still refused — a consumer who cannot "+
			"switch off a shipped rule can only uninstall the plugin:\n%s", got.Output)
	}
	if !e.Exists(proj, newFormatGuardHook) {
		t.Errorf("the write did not land even though nothing refused it")
	}
}

// T026_05: disabling is per RULE, keyed on the qualified name — not per plugin,
// and not shared across natures.
//
// A project that disables `sloprail/gate/authoring-slop` must not thereby
// disable a rule of its own that happens to be called `authoring-slop`. Those are
// different rules with different authors, and conflating them would switch off a
// rule the consumer wrote while they were trying to switch off one they installed.
//
// Here the project's own authoring-slop gate REFUSES, so its being in force
// is visible on the stream. (It shadows the plugin's anyway; the disable of the
// plugin's qualified name must leave the project's untouched.)
func TestT026_05_DisablingAPluginRuleLeavesTheProjectsOwnInForce(t *testing.T) {
	e := New(t)
	proj := e.Project()

	// The project's own rule, named the same, which REFUSES so its being in force
	// is visible on the stream.
	e.Gate(proj, "authoring-slop", shadowingGuardYAML, map[string]string{
		"check.sh": "#!/bin/sh\ncat >/dev/null\necho \"the project's own rule is in force\" >&2\nexit 1\n",
	})
	e.DisablePluginGuardrail(proj, "sloprail/gate/authoring-slop")

	got := e.Run(proj, "s-026-05", "write a guardrail hook", Turns("done",
		Write("w1", ".sloprail/context/other/check.sh", cleanHook),
	))

	if !got.Saw("the project's own rule is in force") {
		t.Fatalf("disabling %q also switched off the PROJECT's own rule of that name — "+
			"the disable list must be keyed on the qualified name:\n%s",
			"sloprail/gate/authoring-slop", got.Output)
	}
}
