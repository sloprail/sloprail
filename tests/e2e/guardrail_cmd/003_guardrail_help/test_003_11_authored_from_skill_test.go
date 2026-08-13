package e2e

import (
	"strings"
	"testing"
)

// T003_11: a guardrail written from nothing but the authoring-guardrails skill
// and `guardrail help` actually fires.
//
// This is the acceptance test for the pair of them. Every other test here checks
// that a particular sentence is true; this checks that the sentences ADD UP to
// enough — that an agent handed the skill for the format and the command for the
// vocabulary can produce a working rule rather than a plausible file. Between
// them they are the authoring interface, so a set of individually accurate
// documents that is collectively insufficient has still failed.
//
// The split is the thing being tested as much as the content: the kind below
// comes from `guardrail help`, everything shaping it comes from the skill, and
// a rule needing both is what proves neither half was left hollow.
//
// The declaration deliberately exercises what an author has to get right from
// reading alone and gets no second chance at:
//
//   - a kind picked off `guardrail help`, with the module attribution ignored
//   - a `list` field matched with `any(...)`, the operator group the string
//     table does not cover
//   - `hooks` as a map to a LIST of bindings, which is the shape most likely to
//     be guessed as a bare mapping
//   - stdin read exactly once, then reused, which is the mistake the skill's
//     hook-script section exists to prevent
//   - a JSON `reason` on stdout with a non-zero exit
//
// It binds to PreCommandInvoke rather than to a file kind on purpose: the file
// kinds are what every other e2e here already covers, so a rule over them could
// pass by resembling one of those instead of by being derivable from the help.
const authoredFromHelp = `---
hooks:
  PreCommandInvoke:
    - matcher: any(invocations, .bin == "curl")
      hooks:
        - type: command
          command: ./no-network.sh
---

# Work is not fetched from the network mid-session

A command that reaches the network makes a session's result depend on something
outside the repository, which no later reader can reproduce.

## Pass
The command runs nothing that fetches over the network.

## Fail
Any invocation in the command line is a network fetcher.
`

// The hook, written from the skill's hook contract section.
//
// `set -uo pipefail` rather than `set -e`, stdin captured once into a variable,
// the refusal as JSON with a reason addressed to the agent, and a non-zero exit
// beside it. The reason does not name the guardrail — the engine appends that.
const noNetworkScript = `#!/usr/bin/env bash
set -uo pipefail
payload="$(cat)"
raw="$(printf '%s' "$payload" | sed -n 's/.*"raw":"\([^"]*\)".*/\1/p')"
printf '{"decision":"block","reason":"This command fetches over the network (%s). Use what is already in the repository, or ask for the data to be committed."}\n' "$raw"
exit 1
`

func TestT003_11_GuardrailAuthoredFromSkillFires(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "no-network-fetch", authoredFromHelp, map[string]string{
		"no-network.sh": noNetworkScript,
	})

	// The load check the skill tells an author to run. A declaration that does
	// not load would make the firing test below fail for the wrong reason, and
	// the help promises this reports it.
	load := e.CLI(proj, "session", "start")
	if strings.Contains(load.Output, "not loaded") {
		t.Fatalf("the declaration written from the skill does not load:\n%s", load.Output)
	}

	got := e.Run(proj, "s-003-11", "fetch the data", Turns("done",
		Bash("b1", "curl -sSL https://example.com/data.json -o data.json"),
	))

	if !got.Saw("fetches over the network") {
		t.Fatalf("a guardrail written from the skill plus `guardrail help` never fired — together they are not sufficient to author against:\n%s", got.Output)
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
// matcher would. The help warns that a list matcher can only be confirmed by
// causing the event; this is the other half of causing it.
func TestT003_12_AuthoredGuardrailLeavesOtherCommands(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "no-network-fetch", authoredFromHelp, map[string]string{
		"no-network.sh": noNetworkScript,
	})

	got := e.Run(proj, "s-003-12", "list the files", Turns("done",
		Bash("b1", "ls -la"),
	))

	if got.Saw("fetches over the network") {
		t.Fatalf("the matcher admitted a command it should not have:\n%s", got.Output)
	}
}
