package e2e

import (
	"strings"
	"testing"
)

// RE-VEHICLED onto the new nature format (was old e.Guardrail / GUARDRAIL.md).
//
// This is the authoring-skill ACCEPTANCE test: it installs the exact declaration
// an agent would author "from nothing but the authoring-guardrails skill" and
// proves it fires. That is only faithful if the installed declaration is the one
// the skill actually teaches — and the skill
// (marketplace/plugins/sloprail/skills/authoring-guardrails/) now teaches the new
// nature format: a rule about a command about to run is a GATE
// (.sloprail/gate/<name>/gate.yaml, gate.md) whose `on` names PreCommandInvoke,
// whose `match` reads the flattened `event.invocations`, and whose check reads the
// FLAT CheckPayload off stdin and refuses with `{"reason":…}` + a non-zero exit.
//
// So the declaration below is the new-format one an author derives from the skill,
// and the installer is e.Gate to match.
//
// T003_11: a guardrail written from nothing but the authoring-guardrails skill
// and the engine's own load check actually fires.
//
// This is the acceptance test for the pair of them. Every other test here checks
// that a particular sentence is true; this checks that the sentences ADD UP to
// enough — that an agent handed the skill for the format and the command for the
// vocabulary can produce a working rule rather than a plausible file. Between
// them they are the authoring interface, so a set of individually accurate
// documents that is collectively insufficient has still failed.
//
// The split is the thing being tested as much as the content: the kind below
// comes from the load check, everything shaping it comes from the skill, and
// a rule needing both is what proves neither half was left hollow.
//
// The declaration deliberately exercises what an author has to get right from
// reading alone and gets no second chance at:
//
//   - a kind picked off the load check's report of what this build produces
//   - a `list` field matched with `any(...)`, the operator group the string
//     table does not cover
//   - the gate's nested `event.*` match scope (gate.md's one asymmetry), so
//     `any(event.invocations, .bin == "curl")` rather than a bare `invocations`
//   - stdin read exactly once, then reused, which is the mistake the skill's
//     check-script section exists to prevent
//   - a JSON `reason` on stdout with a non-zero exit
//
// It binds to PreCommandInvoke rather than to a file kind on purpose: the file
// kinds are what every other e2e here already covers, so a rule over them could
// pass by resembling one of those instead of by being derivable from the skill.
const authoredGate = `on:
  - event: PreCommandInvoke
    match: any(event.invocations, .bin == "curl")
checks:
  - script: ./no-network.sh
`

// The check, written from the skill's check-script and flat-payload sections.
//
// `set -uo pipefail` rather than `set -e`, stdin captured once into a variable,
// the event read FLAT (`.event.invocations`), the refusal as JSON with a reason
// addressed to the agent, and a non-zero exit beside it. The reason does not name
// the guardrail — the engine appends that.
const noNetworkScript = `#!/usr/bin/env bash
set -uo pipefail
payload="$(cat)"
bins="$(printf '%s' "$payload" | jq -r '[.event.invocations[].bin] | join(", ")' 2>/dev/null)"
printf '{"reason":"This command fetches over the network (%s). Use what is already in the repository, or ask for the data to be committed."}\n' "$bins"
exit 1
`

func TestT003_11_GuardrailAuthoredFromSkillFires(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "no-network-fetch", authoredGate, map[string]string{
		"no-network.sh": noNetworkScript,
	})

	// The load check the skill tells an author to run. A declaration that does
	// not load would make the firing test below fail for the wrong reason, and
	// the skill promises this reports it.
	load := e.CLI(proj, "session", "start")
	if strings.Contains(load.Output, "not loaded") {
		t.Fatalf("the declaration written from the skill does not load:\n%s", load.Output)
	}
	// A clean load must say it checked only loading. Silence here was read by a
	// real agent as "my work passes the guardrails", when no rule had run.
	if !strings.Contains(load.Output, "rules loaded. This only checked that they load: no rule ran against any file or action.") {
		t.Errorf("a clean load check does not say that nothing was checked against the work:\n%s", load.Output)
	}

	got := e.Run(proj, "s-003-11", "fetch the data", Turns("done",
		Bash("b1", "curl -sSL https://example.com/data.json -o data.json"),
	))

	if !got.Saw("fetches over the network") {
		t.Fatalf("a guardrail written from the skill plus the load check never fired — together they are not sufficient to author against:\n%s", got.Output)
	}
	// The engine appends the guardrail's name, which the skill says not to
	// include in the reason. If that stopped happening, every refusal would
	// become unattributable and the skill's instruction would be wrong.
	if !got.Saw("no-network-fetch") {
		t.Errorf("the refusal does not name the guardrail, but the skill tells authors to leave the name out because the engine adds it:\n%s", got.Output)
	}
}

// T003_12: the same rule leaves an unrelated command alone.
//
// Without this, T003_11 passes for a matcher that admits everything — which is
// exactly what a mistyped key inside `any(...)` does NOT do, but what a missing
// matcher would. The skill warns that a list matcher can only be confirmed by
// causing the event; this is the other half of causing it.
func TestT003_12_AuthoredGuardrailLeavesOtherCommands(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "no-network-fetch", authoredGate, map[string]string{
		"no-network.sh": noNetworkScript,
	})

	got := e.Run(proj, "s-003-12", "list the files", Turns("done",
		Bash("b1", "ls -la"),
	))

	if got.Saw("fetches over the network") {
		t.Fatalf("the matcher admitted a command it should not have:\n%s", got.Output)
	}
}
