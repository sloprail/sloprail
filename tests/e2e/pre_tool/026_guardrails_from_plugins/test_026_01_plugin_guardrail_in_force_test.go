package e2e

import (
	"strings"
	"testing"
)

// guardrails_from_plugins: the plugin's OWN guardrail, `authoring-slop`, shipped
// inside the installed plugin, is in force in a project that copied nothing.
//
// authoring-slop was migrated from the old GUARDRAIL.md format to the new
// natures: its pre-write half is a PreFileWrite gate at
// marketplace/plugins/sloprail/.sloprail/gate/authoring-slop/ (and a plain
// file-guard of the same name judges the settled file at Stop) — where the
// new plugin-loader reads it (declaration.NewWithPlugins over each enabled
// plugin's `.sloprail`). So this exercises the SAME invariants 026 always proved,
// now against the migrated guardrail and through the NEW nature dispatch:
//
//   - a slop hook (the shape authoring-slop refuses) written under a NEW-format
//     guardrail path is REFUSED, and the refusal NAMES THE PLUGIN and cites the
//     specific rule that decided it;
//   - a clean hook is permitted (the refusal is about the CONTENT, not the path);
//   - a project rule of the same (nature, name) takes precedence (026_03);
//   - a disabled plugin rule is inert, keyed on the qualified name (026_04/05).
//
// Every test here drives the mock, so what fires is the plugin as a user installs
// it: the marketplace source, the enabled plugin, its own hooks.json reaching
// `sr-session pre-tool`, which runs the nature dispatch. Nothing constructs a
// payload by hand — a hand-made one missing the flat `.event.path` or
// SR_GUARDRAIL_DIR makes a shipped hook misbehave and read as a pass, which has
// burned this project repeatedly. Refusals are read through harness.Refused.
//
// The subject is `authoring-slop`, which really does live in the sloprail plugin
// and is never copied into any project these tests create. So "it fired" and "it
// was discovered inside the plugin" are the same fact here; there is no project
// copy that could account for it. The generic new-format plugin-loading mechanism
// is proved in fileguard/035 with a synthetic plugin; 026 keeps its focus on
// authoring-slop's SPECIFIC refusal shapes (the measured slop patterns).

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
// path, so an engine refusing everything under a new-format guardrail dir could
// not pass this file.
const cleanHook = `#!/bin/sh
cat >/dev/null
echo "checked" >&2
exit 0
`

// newFormatGuardHook is where a slop hook lands now: a NEW-format guardrail's own
// script directory. authoring-slop was retargeted from the old
// `.sloprail/guardrails/` prefix to the three new-format nature dirs
// (`.sloprail/{file-guard,gate,context}/<name>/<script>.sh`), so this is the path
// it guards. file-guard/ is chosen here; 026_03 uses gate/ and 026_05 context/ to
// show the retarget covers all three.
const newFormatGuardHook = ".sloprail/file-guard/mine/check.sh"

// T026_01: the headline. A project that installed the plugin and copied nothing
// has authoring-slop enforcing, and the refusal NAMES THE PLUGIN.
//
// The plugin-naming half is the part a bare "was it refused" check would miss.
// A refusal names a guardrail so the reader can go and look at it; for a shipped
// rule the name alone points at the project's own `.sloprail/file-guard/…`, where
// there is nothing — the file is inside an install cache the project never wrote
// to. Without the plugin in the message a user meets a rule they never wrote and
// cannot find.
// sr:proves gates/refusal-names-gate-and-plugin
func TestT026_01_PluginGuardrailFiresAndNamesThePlugin(t *testing.T) {
	e := New(t)
	proj := e.Project()

	// Nothing is written into proj/.sloprail/file-guard/authoring-slop. That
	// absence IS the claim: the only copy of authoring-slop in play is the
	// plugin's own, discovered by the new plugin-loader.

	got := e.Run(proj, "s-026-01", "write a guardrail hook", Turns("done",
		Write("w1", newFormatGuardHook, slopHook),
	))

	if !got.Refused() {
		t.Fatalf("a guardrail shipped in the installed plugin did not refuse — "+
			"nothing discovered it, which is the whole defect this closes:\n%s", got.Output)
	}
	if !strings.Contains(got.Output, "authoring-slop") {
		t.Errorf("the refusal does not name the guardrail:\n%s", got.Output)
	}
	// The refusal must say where the rule came from — a plugin — with the plugin's
	// name, exactly as the migrated new-format guard's Attribution renders
	// (`gate "authoring-slop" from plugin "sloprail"`).
	if !strings.Contains(got.Output, "sloprail") ||
		!strings.Contains(got.Output, "from plugin") {
		t.Errorf("the refusal never says the rule came from a plugin, so a user meets a rule "+
			"they never wrote and cannot find the file:\n%s", got.Output)
	}
	// And it fired for the documented reason, not merely on the path.
	if !strings.Contains(got.Output, "prefer-file-events-over-trajectory") {
		t.Errorf("the refusal does not cite the rule that decided it:\n%s", got.Output)
	}
	if e.Exists(proj, newFormatGuardHook) {
		t.Errorf("the file was written despite the refusal — the work was not actually prevented")
	}
}

// T026_02: the negative control. The same project, the same path, a hook
// carrying none of the refused shapes — permitted.
//
// Without this, T026_01 would pass against an engine that refused every write
// under a new-format guardrail dir for any reason at all, including a bug. This is
// what makes the refusal above evidence about the RULE rather than about the
// path.
func TestT026_02_APluginRuleStillPermitsWhatItDoesNotObjectTo(t *testing.T) {
	e := New(t)
	proj := e.Project()

	// A clean `.sh` passes the grep (check-rules.sh) and so reaches the second
	// check — the judge — which now runs on `.sh` hooks too. "Clean" therefore
	// means passing BOTH the grep AND the judge, so a passing judge verdict is
	// stubbed here (the same InstallJudgeClaude every judge e2e uses). Without it
	// the judge would fail closed on a missing model and this control would refuse
	// for machinery reasons rather than pass on the content.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	got := e.Run(proj, "s-026-02", "write a clean guardrail hook", Turns("done",
		readSkillFirst(t, Write("w1", newFormatGuardHook, cleanHook))...,
	))

	if got.Refused() {
		t.Fatalf("a hook carrying none of the refused shapes was blocked — the plugin's rule "+
			"is refusing on the path rather than on the content:\n%s", got.Output)
	}
	if !e.Exists(proj, newFormatGuardHook) {
		t.Errorf("the permitted write did not land")
	}
}
