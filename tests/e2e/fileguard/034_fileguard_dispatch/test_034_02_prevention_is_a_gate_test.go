package e2e

import (
	"testing"
)

// This file covers WHERE a write is prevented. A file-guard judges the settled
// result at Stop and never acts before a write; refusing a not-fine write BEFORE it
// lands (so it never reaches disk) is a GATE bound to PreFileWrite. The old
// `preventive: true` key on a file-guard was removed — a declaration still carrying
// it is refused at load (declarations/030_07), so the two natures do not overlap.

// preventGate is a PreFileWrite gate over secrets/: a write is not fine if it holds
// a plaintext key. Being a gate, it refuses at pre-tool.
const preventGate = `on:
  - event: PreFileWrite
    match: event.path startsWith "secrets/"
checks:
  - script: ./check.sh
`

// resultGuard is the plain file-guard beside it: the same rule on the settled file.
const resultGuard = `match: "secrets/**"
checks:
  - script: ./check.sh
`

// checkNoPlaintextKey refuses content holding KEY=, on the pre event (a gate) or
// the post event (the file-guard).
const checkNoPlaintextKey = `#!/bin/sh
payload="$(cat)"
if printf '%s' "$payload" | grep -q 'KEY='; then
  echo '{"reason":"a plaintext KEY= must never be written here"}'
  exit 1
fi
exit 0
`

// T034_04: a PreFileWrite gate BLOCKS a not-fine write before it lands.
//
// The write does NOT reach disk — the gate denies at pre-tool. The gate's own
// words reach the agent.
func TestT034_04_GateBlocksPreWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "no-plaintext-keys", preventGate, map[string]string{"check.sh": checkNoPlaintextKey})
	e.CommitAll(proj, "the rule and its scripts")

	res := e.Run(proj, "s-034-04", "write a secret with a plaintext key", Turns("done",
		Write("w1", "secrets/prod.env", "KEY=hunter2"),
	))

	if !res.Refused() {
		t.Fatalf("a PreFileWrite gate did not block the not-fine pre-write:\n%s", res.Output)
	}
	if e.Exists(proj, "secrets/prod.env") {
		t.Errorf("the write LANDED despite the gate refusing it before it landed")
	}
	if !res.Saw("plaintext KEY") {
		t.Errorf("the gate's refusal reason did not reach the agent:\n%s", res.Output)
	}
	if !res.Saw("gate") {
		t.Errorf("the refusal does not say it came from a gate:\n%s", res.Output)
	}
}

// T034_05: the gate ADMITS a fine pre-write — the write lands.
//
// The control for T034_04.
func TestT034_05_GateAdmitsFineWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "no-plaintext-keys", preventGate, map[string]string{"check.sh": checkNoPlaintextKey})
	e.CommitAll(proj, "the rule and its scripts")

	res := e.Run(proj, "s-034-05", "write a clean secret reference", Turns("done",
		Write("w1", "secrets/prod.env", "KEY_REF=vault://prod"),
	))

	if res.Refused() {
		t.Fatalf("a gate refused a fine write:\n%s", res.Output)
	}
	if !e.Exists(proj, "secrets/prod.env") {
		t.Errorf("a fine write the gate admitted did not land")
	}
}

// T034_06: a file-guard does NOT block the pre-write — it only checks the settled
// file. The write lands (the after-check then blocks the turn, but the write is
// not prevented).
//
// The control that proves the gate is what makes the difference: the SAME check on
// a file-guard lets the write reach disk.
func TestT034_06_FileGuardDoesNotBlockPreWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "no-plaintext-keys", resultGuard, map[string]string{"check.sh": checkNoPlaintextKey})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the rule and its scripts")

	res := e.Run(proj, "s-034-06", "write a secret with a plaintext key", Turns("done",
		Write("w1", "secrets/prod.env", "KEY=hunter2"),
	))

	// The pre-tool call is NOT denied (a file-guard does not act at pre-tool)...
	if res.Refused() {
		t.Errorf("a file-guard blocked the pre-write — only a gate should:\n%s", res.Output)
	}
	// ...so the write LANDS.
	if !e.Exists(proj, "secrets/prod.env") {
		t.Errorf("a file-guard prevented a write it should only have checked after")
	}
	// The after-check at Stop still catches it and blocks the turn.
	if len(e.BlockingErrorsFrom(proj, "s-034-06", "Stop")) == 0 {
		t.Errorf("the file-guard's after-check did not block the turn on a not-fine file")
	}
}

// T034_07: the split holds together — the gate refuses the not-fine write before
// it lands, and the plain file-guard of the same name, beside it, judges what
// settles and passes a fine one. A bad write is prevented, a good one lands and is
// left alone at Stop.
func TestT034_07_GateAndFileGuardOfOneRuleWorkTogether(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "no-plaintext-keys", preventGate, map[string]string{"check.sh": checkNoPlaintextKey})
	e.FileGuard(proj, "no-plaintext-keys", resultGuard, map[string]string{"check.sh": checkNoPlaintextKey})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the rule and its scripts")

	bad := e.Run(proj, "s-034-07a", "write a secret with a plaintext key", Turns("done",
		Write("w1", "secrets/prod.env", "KEY=hunter2"),
	))
	if !bad.Refused() || e.Exists(proj, "secrets/prod.env") {
		t.Fatalf("the not-fine write was not prevented before it landed:\n%s", bad.Output)
	}

	good := e.Run(proj, "s-034-07b", "write a clean secret reference", Turns("done",
		Write("w2", "secrets/prod.env", "KEY_REF=vault://prod"),
	).ThenCommit("add the reference"))
	if good.Refused() || !e.Exists(proj, "secrets/prod.env") {
		t.Fatalf("the fine write was refused or did not land:\n%s", good.Output)
	}
	if n := len(e.BlockingErrorsFrom(proj, "s-034-07b", "Stop")); n != 0 {
		t.Errorf("the file-guard blocked the turn on a fine settled file (%d blocks)", n)
	}
}
