package e2e

import (
	"strings"
	"testing"
)

// T031_09: normalize's default read is the part of the session not yet judged,
// and --whole-session widens it to the whole record.
//
// This is the one behaviour that needs a real session rather than a fixture: the
// slice is defined by the read mark, which only a running session advances. So it
// drives the mock harness across two cycles, the same shape 018 uses for `query`.
//
// The mark advances the same way it does for `query` — it is a fact about THIS
// session, kept in this session's own store, moved when a cycle completes over
// what `query` recorded it read. So the hook runs `query` first (which advances
// the mark, as the cycle's primary read), then runs `normalize` twice: once by
// default, once with --whole-session, recording each. By the second cycle the
// mark sits past the first cycle's turns, so:
//
//   - the DEFAULT normalize read must NOT carry the first cycle's marker, and
//   - the --whole-session read MUST.
//
// A guardrail hook is handed `{event, guardrailDir}`, not the harness payload, so
// it reconstructs the payload from SR_TRANSCRIPT/SR_WORKSPACE the way the shipped
// examples do — the same idiom 018's askScript uses.

// sliceWatch binds to a Post kind so it runs at a cycle's end, and records both a
// default and a whole-session normalize read of the session.
const sliceWatch = `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./slice.sh
  PostFileUpdate:
    - hooks:
        - type: command
          command: ./slice.sh
---

# Reads the session both ways and records the command lines each returned
`

// sliceScript advances the mark through `query`, then records what `normalize`
// returns by default and with --whole-session. The `raw` command line of each
// PreCommandInvoke is the distinctive token the assertions look for — a cheap way
// to ask "did this read include cycle N's Bash turn".
const sliceScript = `#!/bin/sh
cat > /dev/null
if [ -z "${SR_TRANSCRIPT:-}" ]; then
  echo "SR_TRANSCRIPT unset" >> "$PWD/whole"
  echo "SR_TRANSCRIPT unset" >> "$PWD/scoped"
  exit 0
fi
payload='{"transcript_path":"'"$SR_TRANSCRIPT"'","cwd":"'"$SR_WORKSPACE"'"}'
# Advance the mark as the cycle's primary read does.
printf '%s' "$payload" | sr-session query > /dev/null 2>&1
# The default read — the part not yet judged.
printf '%s' "$payload" | sr-session trajectory normalize >> "$PWD/scoped" 2>&1
printf '\n===CYCLE===\n' >> "$PWD/scoped"
# The whole record.
printf '%s' "$payload" | sr-session trajectory normalize --whole-session >> "$PWD/whole" 2>&1
printf '\n===CYCLE===\n' >> "$PWD/whole"
exit 0
`

func TestT031_09_DefaultSliceSkipsJudgedTurnsAndWholeSessionDoesNot(t *testing.T) {
	e := New(t)
	proj := e.Project()
	// A repository, so the cycle has a baseline and its Post file event fires the
	// hook — the same setup 018 needs.
	e.GitInit(proj)
	e.Guardrail(proj, "slicer", sliceWatch, map[string]string{"slice.sh": sliceScript})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "before the session")

	const sess = "s-031-09"
	// Distinctive command lines, one per cycle, that show up as a PreCommandInvoke
	// raw field — the token each read is searched for. Each cycle also writes a
	// file so the Post-bound hook fires.
	e.Run(proj, sess, "first cycle", Turns("done",
		Bash("b1", "echo CYCLEONECOMMAND"),
		Write("w1", "one.md", "first\n"),
	))
	e.Run(proj, sess, "second cycle", Turns("done",
		Bash("b2", "echo CYCLETWOCOMMAND"),
		Write("w2", "two.md", "second\n"),
	))

	scoped := strings.Join(e.Ledger(proj, "slicer", "scoped"), "\n")
	whole := strings.Join(e.Ledger(proj, "slicer", "whole"), "\n")
	if scoped == "" || whole == "" {
		t.Fatalf("the hook never recorded a read, so nothing here can be observed\nscoped:\n%s\nwhole:\n%s", scoped, whole)
	}
	if strings.Contains(scoped, "sloprail:") {
		t.Fatalf("the default normalize read errored, so the slice below is vacuous:\n%s", scoped)
	}

	// Split each ledger into per-cycle reads. The LAST cycle's read is what the
	// assertion is about: by then the mark sits past cycle one.
	scopedCycles := splitCycles(scoped)
	wholeCycles := splitCycles(whole)
	if len(scopedCycles) < 2 || len(wholeCycles) < 2 {
		t.Fatalf("expected at least two recorded cycles; got scoped=%d whole=%d\nscoped:\n%s",
			len(scopedCycles), len(wholeCycles), scoped)
	}
	lastScoped := scopedCycles[len(scopedCycles)-1]
	lastWhole := wholeCycles[len(wholeCycles)-1]

	// The control: the second cycle's own command is in both reads. Without it the
	// "absent" assertion below would be satisfied by an empty read.
	if !strings.Contains(lastScoped, "CYCLETWOCOMMAND") {
		t.Fatalf("the default read of the last cycle did not carry its own turn — the slice assertion would be vacuous:\n%s", lastScoped)
	}

	// The invariant: the default read skips the first cycle's already-judged turn.
	if strings.Contains(lastScoped, "CYCLEONECOMMAND") {
		t.Fatalf("the default normalize read handed back a turn the first cycle already judged:\n%s\n"+
			"the default read is the part not yet judged, like `query`", lastScoped)
	}

	// And --whole-session widens back to the whole record — the first cycle's turn
	// is present there.
	if !strings.Contains(lastWhole, "CYCLEONECOMMAND") {
		t.Fatalf("--whole-session did not widen to the whole record — the first cycle's turn is missing:\n%s", lastWhole)
	}
}

// splitCycles breaks a ledger into the per-invocation reads the hook separated
// with a ===CYCLE=== marker, dropping empty trailing segments.
func splitCycles(ledger string) []string {
	var out []string
	for _, seg := range strings.Split(ledger, "===CYCLE===") {
		if strings.TrimSpace(seg) != "" {
			out = append(out, seg)
		}
	}
	return out
}
