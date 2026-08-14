package e2e

import (
	"strings"
	"testing"
)

// guardrails_from_plugins: a guardrail shipped inside an installed plugin is in
// force in a project that copied nothing.
//
// Every test here drives the mock, so what fires is the plugin as a user
// installs it: the marketplace source, the enabled plugin, its own hooks.json
// reaching `sr-session pre-tool`. Nothing constructs a payload by hand. That is
// not a stylistic preference — a hand-made payload missing `guardrailDir` makes
// a shipped hook fail open and read as a pass, which has burned this project
// repeatedly. Refusals are read through harness.Refused, which matches the
// harness's own marker rather than scanning for words.
//
// The subject is `authoring-slop`, which really does live in
// marketplace/plugins/sloprail/guardrails/ and is never copied into any project
// these tests create. So "it fired" and "it was discovered inside the plugin"
// are the same fact here; there is no project copy that could account for it.

// slopHook is a guardrail hook script carrying the shape authoring-slop refuses:
// a tool-NAME allowlist. Rule `prefer-file-events-over-trajectory` — the names
// go stale silently, which is how a rule stops seeing turns without erroring.
//
// Deliberately the measured signature (two tool names joined by a pipe) rather
// than something merely suspicious, so this fires for the documented reason
// rather than by accident.
const slopHook = `#!/bin/sh
cat >/dev/null
tool=$(printf '%s' "$payload" | grep -E '"(Write|Edit|MultiEdit)"')
exit 0
`

// cleanHook carries none of the refused shapes. The negative control: it proves
// the refusals below are about the CONTENT of what was written, not about the
// path, so an engine refusing everything under .sloprail/guardrails/ could not
// pass this file.
const cleanHook = `#!/bin/sh
cat >/dev/null
echo "checked" >&2
exit 0
`

// T026_01: the headline. A project that installed the plugin and copied nothing
// has authoring-slop enforcing, and the refusal NAMES THE PLUGIN.
//
// The plugin-naming half is the part a bare "was it refused" check would miss,
// and it is question 3 of the task. A refusal names a guardrail so the reader
// can go and look at it; for a shipped rule the name alone points at
// .sloprail/guardrails/authoring-slop, where there is nothing — the file is
// inside an install cache the project never wrote to. Without the plugin in the
// message a user meets a rule they never wrote and cannot find.
func TestT026_01_PluginGuardrailFiresAndNamesThePlugin(t *testing.T) {
	e := New(t)
	proj := e.Project()

	// Nothing is written into proj/.sloprail/guardrails/. That absence IS the
	// claim: the only copy of authoring-slop in play is the plugin's own.

	got := e.Run(proj, "s-026-01", "write a guardrail hook", Turns("done",
		Write("w1", ".sloprail/guardrails/mine/check.sh", slopHook),
	))

	if !got.Refused() {
		t.Fatalf("a guardrail shipped in the installed plugin did not refuse — "+
			"nothing discovered it, which is the whole defect this closes:\n%s", got.Output)
	}
	if !strings.Contains(got.Output, "authoring-slop") {
		t.Errorf("the refusal does not name the guardrail:\n%s", got.Output)
	}
	// Question 3. The refusal must say where the rule came from.
	if !strings.Contains(got.Output, "sloprail") ||
		!strings.Contains(got.Output, "from plugin") {
		t.Errorf("the refusal never says the rule came from a plugin, so a user meets a rule "+
			"they never wrote and cannot find the file:\n%s", got.Output)
	}
	// And it fired for the documented reason, not merely on the path.
	if !strings.Contains(got.Output, "prefer-file-events-over-trajectory") {
		t.Errorf("the refusal does not cite the rule that decided it:\n%s", got.Output)
	}
	if e.Exists(proj, ".sloprail/guardrails/mine/check.sh") {
		t.Errorf("the file was written despite the refusal — the work was not actually prevented")
	}
}

// T026_02: the negative control. The same project, the same path, a hook
// carrying none of the refused shapes — permitted.
//
// Without this, T026_01 would pass against an engine that refused every write
// under .sloprail/guardrails/ for any reason at all, including a bug. This is
// what makes the refusal above evidence about the RULE rather than about the
// path.
func TestT026_02_APluginRuleStillPermitsWhatItDoesNotObjectTo(t *testing.T) {
	e := New(t)
	proj := e.Project()

	got := e.Run(proj, "s-026-02", "write a clean guardrail hook", Turns("done",
		Write("w1", ".sloprail/guardrails/mine/check.sh", cleanHook),
	))

	if got.Refused() {
		t.Fatalf("a hook carrying none of the refused shapes was blocked — the plugin's rule "+
			"is refusing on the path rather than on the content:\n%s", got.Output)
	}
	if !e.Exists(proj, ".sloprail/guardrails/mine/check.sh") {
		t.Errorf("the permitted write did not land")
	}
}
