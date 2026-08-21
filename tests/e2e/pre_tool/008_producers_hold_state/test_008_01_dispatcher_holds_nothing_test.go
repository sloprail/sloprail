package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// producers_hold_state: whatever must be remembered to report an event honestly
// is remembered by whatever produces that event, not by the hook point that
// dispatches it. The observable form: the dispatch carries nothing kind-specific —
// what it hands a check is the event and the session facts, the same surface for
// every kind, so adding a kind costs it nothing.
//
// # RE-VEHICLED onto the NEW file-guard nature (was old GUARDRAIL.md hooks)
//
// This is SHARED machinery the new dispatch reuses: the per-guard `sr-session
// state` keyspace, the environment a check runs in, and the fixed payload surface.
// The old rules installed via `e.Guardrail` and read the NESTED payload; these
// install NEW-format file-guards (`e.FileGuard`) and read the FLAT CheckPayload,
// whose surface is `{event, transcriptPath, context}` by construction
// (internal/declaration/payload.go). A file-guard's check runs in the guard's own
// folder with SR_GUARDRAIL set to the guard's name and SR_GUARDRAIL_DIR to its
// folder (internal/dispatch/exec.go), so `sr-session state` reaches the guard's own
// keyspace exactly as an old hook's did — which is why the state tests in this
// directory re-vehicle without weakening.
//
// The guard here is PREVENTIVE and RECORDS then PERMITS, so both a create and an
// update are observed (the first write must land for the second to be an update).
// The permit means the Stop after-check records again; every recorded line is
// asserted to have the same surface regardless, which is exactly the "carries
// nothing kind-specific" claim.

const recordPayload = `match: "**/*.md"
preventive: true
checks:
  - script: ./record.sh
`

// The check records the whole payload it is handed into the guard's own folder
// (SR_GUARDRAIL_DIR — not $PWD, which a file-guard check does not have pointed at
// its folder), then permits so the write lands and the next one is an update.
const recordScript = `#!/bin/sh
cat >> "$SR_GUARDRAIL_DIR/seen"
echo >> "$SR_GUARDRAIL_DIR/seen"
exit 0
`

// T008_01: the dispatch hands over the event and nothing kind-specific.
//
// Two different kinds through the same dispatch, and the payload's shape must not
// vary with the kind. A dispatch that had learned what a file event is — carrying a
// baseline, a prior verdict, a per-kind slot — would show that here as a key
// present for one kind and absent for another, or as a key describing something
// other than the occurrence.
func TestT008_01_DispatcherCarriesNothingKindSpecific(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "recorder", recordPayload, map[string]string{"record.sh": recordScript})

	// The first write creates; the second updates the file the first left behind.
	e.Run(proj, "s-008-01", "write twice", Turns("done",
		Write("w1", "notes.md", "hello"),
		Write("w2", "notes.md", "hello again"),
	))

	lines := e.FileGuardLedgerLines(proj, "recorder", "seen")
	if len(lines) < 2 {
		t.Fatalf("want at least one dispatch per write, got %d: %v", len(lines), lines)
	}

	kinds := map[string]bool{}
	for i, line := range lines {
		var shape map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &shape); err != nil {
			t.Fatalf("dispatch %d is not an object: %v\n%s", i, err, line)
		}
		// The dispatch's whole surface, identical for every kind. Anything else is
		// the hook point holding something on a kind's behalf.
		for key := range shape {
			if key != "event" && key != "transcriptPath" && key != "context" {
				t.Errorf("the dispatch carries %q alongside the event and session facts — state a producer should hold:\n%s", key, line)
			}
		}
		var p struct {
			Event struct {
				Kind string `json:"kind"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("dispatch %d carries no event: %v", i, err)
		}
		kinds[p.Event.Kind] = true
	}

	// The dispatches must genuinely span more than one kind — a create and an update
	// at least — or the surface check above compared one kind against itself and
	// proved nothing.
	var creates, updates int
	for k := range kinds {
		switch {
		case strings.Contains(k, "Create"):
			creates++
		case strings.Contains(k, "Update"):
			updates++
		}
		if !strings.HasPrefix(k, "PreFile") && !strings.HasPrefix(k, "PostFile") {
			t.Errorf("unexpected kind %q", k)
		}
	}
	if creates == 0 || updates == 0 {
		t.Fatalf("the payload shape was not compared across differing kinds: saw kinds %v", keysOf(kinds))
	}
}

// keysOf lists a set's keys, for a failure message.
func keysOf(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
