package e2e

import (
	"strings"
	"testing"
)

// One guardrail, two hooks in declaration order. The first plants a registry
// under a DIFFERENT guardrail's name; the second reads it back with
// `state list --owner <that name>` and reports what it saw.
//
// Two hooks of one rule rather than two rules because between guardrails nothing
// is promised about order, but hooks within a binding run in declaration order —
// so the second hook genuinely runs after the registry exists. The owner name
// the second hook reads is one the reading guardrail never wrote under, so a
// non-empty read is the cross-guardrail read working, not its own entries coming
// back.
//
// Planting the owner's rows by naming it on the way in is legitimate and already
// pinned by T008_04: a hook is an arbitrary command and may set SR_GUARDRAIL
// before calling the CLI. This test uses that only to stand a sibling context's
// registry up deterministically; the behaviour under test is the READER's
// --owner.
const ownerReadGuardrail = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "owner/"
      hooks:
        - type: command
          command: ./plant.sh
        - type: command
          command: ./read.sh
---

# Reads a sibling context's registry with --owner

The context logs each subject under its own name; this gate reads the group
back by naming that context as --owner. Ordering across the two is the caller's
to establish with require: [{context}]; here the two hooks run in order so the
registry is present when the gate reads it.
`

// Plants a two-entry registry under the owner's name, and its OWN distinct entry
// under the reading guardrail's real name. The own entry lets the reader prove
// its own-scoped list is separate from what --owner returns.
//
// It refuses nothing — it exits zero so the second hook runs. A silent failure
// here would make the reader's assertions vacuous, so each write is guarded.
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
// The KEYS are asserted, extracted with `jq -r .key` and comma-joined. Keys are
// plain tokens (reg:alpha, own:mine) that survive the hook's stderr becoming a
// JSON string in the tool_result, whereas the values carry quotes and
// backslashes that get re-escaped in transport and are painful to match.
//
// To prove the VALUE genuinely crosses the boundary too — not only the key — the
// reader also reads one owned value directly and reports it. That value is picked
// to be quote-free (a bare token) so it matches cleanly through transport.
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
// This is the capability the shipped gates need — a gate cross-referencing the
// registry a context accumulated — exercised through the real CLI.
//
// Three facts are asserted together, because --owner is only correct if all
// three hold: it returns the OWNER's entries under the prefix; an owner that
// never wrote is EMPTY not an error; and the reader's OWN list is untouched by
// --owner, still its own entry and not the owner's.
func TestT008_07_OwnerReadsSiblingRegistry(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "gate", ownerReadGuardrail, map[string]string{
		"plant.sh": ownerPlantScript, "read.sh": ownerReadScript,
	})

	got := e.Run(proj, "s-008-07", "write a note", Turns("done",
		Write("w1", "owner/notes.md", "hello"),
	))

	// The registry has to have been planted, or an empty owned read proves
	// nothing about isolation vs. a broken write.
	if got.Saw("PLANT-FAILED") {
		t.Fatalf("the sibling registry could not be planted, so the owned read proves nothing:\n%s", got.Output)
	}
	if !got.Saw("OWNED-KEYS:") {
		t.Fatalf("the reader hook never ran, so nothing was tested:\n%s", got.Output)
	}

	// The cross-guardrail read returns the owner's two reg: keys, in key order,
	// under the prefix. The reading guardrail never wrote under registry-owner,
	// so these are the owner's rows and not its own.
	if !got.Saw("OWNED-KEYS:[reg:alpha,reg:beta]") {
		t.Fatalf("--owner did not return the sibling's registry keys under the prefix:\n%s", got.Output)
	}
	// The VALUE crosses too, not only the key: the owner's sig: entry reads back
	// as the exact value the owner stored.
	if !got.Saw("OWNED-VAL:[owner-signature]") {
		t.Fatalf("--owner returned the key but not the owner's stored value:\n%s", got.Output)
	}

	// An owner that never wrote is an empty read, exiting zero — not an error the
	// gate would have to tell apart from "nothing declared".
	if !got.Saw("MISSING:[]") {
		t.Fatalf("--owner on a guardrail that never wrote was not empty:\n%s", got.Output)
	}

	// The reader's OWN list is unaffected by --owner: it still sees its own entry
	// and NOT the owner's. The own read returns own:mine alone — none of the
	// owner's keys, so the cross-guardrail read did not leak into the plain one.
	if !got.Saw("MINE-KEYS:[own:mine]") {
		t.Fatalf("the reader's own list was not its own entry alone — --owner may have leaked into the plain read:\n%s", got.Output)
	}
	if mine := lineWithPrefix(got.Output, "MINE-KEYS:"); strings.Contains(mine, "reg:") || strings.Contains(mine, "sig:") {
		t.Fatalf("the reader's own list contained the owner's registry keys — the boundary leaked:\n%s", got.Output)
	}
}

// lineWithPrefix returns the substring of output starting at prefix and ending
// at the next newline (or end), or "" when the prefix is absent. It reads the
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
