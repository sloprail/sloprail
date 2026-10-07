package e2e

import (
	"strings"
	"testing"
)

// One guardrail, two checks in declaration order. The first plants a registry
// under a DIFFERENT guardrail's name; the second reads it back with `state list
// --owner <that name>` and reports what it saw. SHARED machinery — the
// cross-guard `--owner` read a shipped gate uses to cross-reference a context's
// registry, exercised through the real CLI against a NEW-format gate.
//
// Two checks of one guard rather than two guards because the gate
// dispatch runs gates in name order but stops at the first refusal — two checks
// of one guard run in declaration order, so the reader genuinely runs after the
// registry exists. Planting the owner's rows by naming it on the way in is
// legitimate and already pinned by T008_04: a check is an arbitrary command and may
// set SR_GUARDRAIL before calling the CLI. The behaviour under test is the READER's
// --owner.
const ownerReadGuard = `on:
  - event: PreFileWrite
    match: event.path startsWith "owner/"
checks:
  - script: ./plant.sh
  - script: ./read.sh
`

// Plants a two-entry registry under the owner's name, and its OWN distinct entry
// under the reading guardrail's real name. The own entry lets the reader prove its
// own-scoped list is separate from what --owner returns. It permits (exit 0) so the
// second check runs.
const ownerPlantScript = `#!/bin/sh
cat >/dev/null
SR_GUARDRAIL=registry-owner sr-session state set "reg:alpha" '{"kw":["x"]}' || { echo "PLANT-FAILED-alpha" >&2; exit 1; }
SR_GUARDRAIL=registry-owner sr-session state set "reg:beta"  '{"kw":["y"]}' || { echo "PLANT-FAILED-beta" >&2; exit 1; }
SR_GUARDRAIL=registry-owner sr-session state set "sig:one" "owner-signature" || { echo "PLANT-FAILED-sig" >&2; exit 1; }
sr-session state set "own:mine" "reader-value" || { echo "PLANT-FAILED-own" >&2; exit 1; }
exit 0
`

// Reads three ways and refuses with all three, so every read reaches the test:
//   - --owner registry-owner reg:   the cross-guardrail read under a prefix
//   - --owner does-not-exist        an owner that never wrote -> empty
//   - (own list, no --owner)        the reader's own entries, unaffected
//
// The KEYS are asserted, extracted with `jq -r .key`. To prove the VALUE genuinely
// crosses too, the reader also reads one owned value directly (a quote-free token
// that matches cleanly through transport).
const ownerReadScript = `#!/bin/sh
cat >/dev/null
owned_keys="$(sr-session state list --owner registry-owner "reg:" | jq -r .key | paste -sd, -)"
owned_val="$(sr-session state list --owner registry-owner "sig:" | jq -r .value | paste -sd, -)"
missing="$(sr-session state list --owner nobody-wrote-here | jq -r .key | paste -sd, -)"
mine_keys="$(sr-session state list | jq -r .key | paste -sd, -)"
echo "OWNED-KEYS:[$owned_keys]" >&2
echo "OWNED-VAL:[$owned_val]" >&2
echo "MISSING:[$missing]" >&2
echo "MINE-KEYS:[$mine_keys]" >&2
exit 1
`

// T008_07: a guardrail reads a sibling's registry with --owner.
//
// The reader is a different guardrail from the one whose rows it reads: it reads
// registry-owner's entries though it never wrote under that name, and gets them.
// Three facts are asserted together, because --owner is only correct if all three
// hold: it returns the OWNER's entries under the prefix; an owner that never wrote
// is EMPTY not an error; and the reader's OWN list is untouched by --owner.
func TestT008_07_OwnerReadsSiblingRegistry(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "gate", ownerReadGuard, map[string]string{
		"plant.sh": ownerPlantScript, "read.sh": ownerReadScript,
	})

	got := e.Run(proj, "s-008-07", "write a note", Turns("done",
		Write("w1", "owner/notes.md", "hello"),
	))

	// The registry has to have been planted, or an empty owned read proves nothing.
	if got.Saw("PLANT-FAILED") {
		t.Fatalf("the sibling registry could not be planted, so the owned read proves nothing:\n%s", got.Output)
	}
	if !got.Saw("OWNED-KEYS:") {
		t.Fatalf("the reader check never ran, so nothing was tested:\n%s", got.Output)
	}

	// The cross-guardrail read returns the owner's two reg: keys, in key order,
	// under the prefix. The reading guardrail never wrote under registry-owner, so
	// these are the owner's rows and not its own.
	if !got.Saw("OWNED-KEYS:[reg:alpha,reg:beta]") {
		t.Fatalf("--owner did not return the sibling's registry keys under the prefix:\n%s", got.Output)
	}
	// The VALUE crosses too, not only the key.
	if !got.Saw("OWNED-VAL:[owner-signature]") {
		t.Fatalf("--owner returned the key but not the owner's stored value:\n%s", got.Output)
	}
	// An owner that never wrote is an empty read, exiting zero — not an error the
	// gate would have to tell apart from "nothing declared".
	if !got.Saw("MISSING:[]") {
		t.Fatalf("--owner on a guardrail that never wrote was not empty:\n%s", got.Output)
	}
	// The reader's OWN list is unaffected by --owner: it still sees its own entry
	// and NOT the owner's.
	if !got.Saw("MINE-KEYS:[own:mine]") {
		t.Fatalf("the reader's own list was not its own entry alone — --owner may have leaked into the plain read:\n%s", got.Output)
	}
	if mine := lineWithPrefix(got.Output, "MINE-KEYS:"); strings.Contains(mine, "reg:") || strings.Contains(mine, "sig:") {
		t.Fatalf("the reader's own list contained the owner's registry keys — the boundary leaked:\n%s", got.Output)
	}
}

// lineWithPrefix returns the substring of output starting at prefix and ending at
// the next escaped newline (or end), or "" when the prefix is absent. It reads the
// escaped transport blob, where lines are joined by literal \n rather than real
// newlines, so it splits on that.
func lineWithPrefix(output, prefix string) string {
	i := strings.Index(output, prefix)
	if i < 0 {
		return ""
	}
	rest := output[i:]
	if j := strings.Index(rest, `\n`); j >= 0 {
		return rest[:j]
	}
	return rest
}
