package e2e

import (
	"testing"
)

// This file covers a PREVENTIVE file-guard: `preventive: true` makes the guard
// fire on the PRE file event too, refusing a not-fine write BEFORE it lands (via
// the pre-tool deny, so the write never reaches disk). The after-check at Stop is
// still there — preventive is an ADDITION to it — but what this file pins is the
// before-block.

// preventiveGuard is a preventive file-guard over src/ .env files: a write is not
// fine if it holds a plaintext key. Being preventive, it refuses at pre-tool.
const preventiveGuard = `match: "secrets/**"
preventive: true
checks:
  - script: ./check.sh
`

// checkNoPlaintextKey refuses newContent holding KEY=, on the pre event, so the
// write is stopped before it lands.
const checkNoPlaintextKey = `#!/bin/sh
payload="$(cat)"
if printf '%s' "$payload" | grep -q 'KEY='; then
  echo '{"reason":"a plaintext KEY= must never be written here"}'
  exit 1
fi
exit 0
`

// T034_04: a preventive guard BLOCKS a not-fine write before it lands.
//
// The write does NOT reach disk — a preventive guard denies at pre-tool, the same
// as a gate. The guard's own words reach the agent.
func TestT034_04_PreventiveBlocksPreWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "no-plaintext-keys", preventiveGuard, map[string]string{"check.sh": checkNoPlaintextKey})

	res := e.Run(proj, "s-034-04", "write a secret with a plaintext key", Turns("done",
		Write("w1", "secrets/prod.env", "KEY=hunter2"),
	))

	if !res.Refused() {
		t.Fatalf("a preventive file-guard did not block the not-fine pre-write:\n%s", res.Output)
	}
	if e.Exists(proj, "secrets/prod.env") {
		t.Errorf("the write LANDED despite the preventive guard refusing it before it landed")
	}
	if !res.Saw("plaintext KEY") {
		t.Errorf("the preventive guard's refusal reason did not reach the agent:\n%s", res.Output)
	}
}

// T034_05: a preventive guard ADMITS a fine pre-write — the write lands.
//
// The control for T034_04.
func TestT034_05_PreventiveAdmitsFineWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "no-plaintext-keys", preventiveGuard, map[string]string{"check.sh": checkNoPlaintextKey})

	res := e.Run(proj, "s-034-05", "write a clean secret reference", Turns("done",
		Write("w1", "secrets/prod.env", "KEY_REF=vault://prod"),
	))

	if res.Refused() {
		t.Fatalf("a preventive guard refused a fine write:\n%s", res.Output)
	}
	if !e.Exists(proj, "secrets/prod.env") {
		t.Errorf("a fine write the preventive guard admitted did not land")
	}
}

// T034_06: a NON-preventive guard does NOT block the pre-write — it only checks
// after. The write lands (the after-check then blocks the turn, but the write is
// not prevented).
//
// The control that proves preventive is what makes the difference: the SAME check
// on a non-preventive guard lets the write reach disk.
func TestT034_06_NonPreventiveDoesNotBlockPreWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	// Same check, but NOT preventive.
	nonPreventive := `match: "secrets/**"
checks:
  - script: ./check.sh
`
	e.FileGuard(proj, "no-plaintext-keys", nonPreventive, map[string]string{"check.sh": checkNoPlaintextKey})

	res := e.Run(proj, "s-034-06", "write a secret with a plaintext key", Turns("done",
		Write("w1", "secrets/prod.env", "KEY=hunter2"),
	))

	// The pre-tool call is NOT denied (non-preventive does not act at pre-tool)...
	if res.Refused() {
		t.Errorf("a NON-preventive guard blocked the pre-write — only preventive should:\n%s", res.Output)
	}
	// ...so the write LANDS.
	if !e.Exists(proj, "secrets/prod.env") {
		t.Errorf("a non-preventive guard prevented a write it should only have checked after")
	}
	// The after-check at Stop still catches it and blocks the turn.
	if len(e.BlockingErrorsFrom(proj, "s-034-06", "Stop")) == 0 {
		t.Errorf("the non-preventive guard's after-check did not block the turn on a not-fine file")
	}
}
