package e2e

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// `seen` through the wiring a user gets: a Stop refused once, and the retry.
//
// A cycle stays open until a Stop passes, so the retry's Post events re-send
// what the refused reply already delivered: its tags, and every file still in
// the difference. `seen: true` marks those re-sends, and only those — a tag the
// retry itself writes, or a file changed since the previous Stop, is unseen.
//
// The scenario: the agent writes notes.md in a message that also says `#alpha`,
// then ends its turn with a prose-only message saying `#beta`. A Stop gate
// refuses the first Stop only. The mock re-runs the refused turn: the tool turn
// is marked done and not repeated, but the prose-only message is emitted again
// (same text, same uuid) — so at the retry's Stop `#alpha` is re-sent (seen),
// `#beta` was written again (unseen, though it was also in the refused reply),
// and notes.md is unchanged (seen).
//
// A context records every Post event it is handed, per Stop, to a log outside
// the project (so the log is no file change of its own). The first Stop's entries
// must all be unseen — nothing was shown to an earlier Stop — which is what
// separates this from a flag stuck on true.

const seenWatch = `on:
  - event: PostTagWrite
  - event: PostFileWrite
enter: ./enter.sh
exit: ./exit.sh
`

// refuseFirstStop refuses the first Stop it sees and permits every later one,
// using a marker outside the project.
const refuseFirstStop = `on:
  - event: Stop
checks:
  - script: ./check.sh
`

func TestT030_03_ARetrysPostEventsMarkWhatTheRefusedStopWasShown(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	scratch := t.TempDir()
	logPath := filepath.Join(scratch, "events.jsonl")
	stopsPath := filepath.Join(scratch, "stops")
	marker := filepath.Join(scratch, "refused-once")

	// Every enter appends the event it was handed, stamped with how many Stops
	// the gate has seen so far (the gate runs after the enters, so this is the
	// number of the Stop in progress minus one).
	e.Context(proj, "seen-watch", seenWatch, map[string]string{
		"enter.sh": "#!/bin/sh\nn=$(cat " + stopsPath + " 2>/dev/null || echo 0)\n" +
			"jq -c --argjson stop \"$n\" '.event + {stop: $stop}' >> " + logPath + "\nexit 0\n",
		"exit.sh": exitStayActive,
	})
	e.Gate(proj, "refuse-first", refuseFirstStop, map[string]string{
		"check.sh": "#!/bin/sh\ncat >/dev/null\nn=$(cat " + stopsPath + " 2>/dev/null || echo 0)\n" +
			"echo $((n + 1)) > " + stopsPath + "\n" +
			"if [ ! -e " + marker + " ]; then touch " + marker + "; echo '{\"reason\":\"not yet\"}'; exit 1; fi\nexit 0\n",
	})
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-030-03", "do the work", Turns("done",
		SayWrite("w1", "Writing the notes. #alpha", "notes.md", "the notes\n"),
		Say("m2", "Done. #beta"),
	))

	byStop := map[int][]map[string]any{}
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("the context never recorded an event: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("unreadable log line %q: %v", sc.Text(), err)
		}
		n := int(ev["stop"].(float64))
		byStop[n] = append(byStop[n], ev)
	}
	if !harness.HasCap(t, harness.CapScriptedRetryText) {
		// This harness's agent cannot be scripted to answer the refusal with text of its own: the
		// one reply it gives is the text the refused Stop was shown, so at the retry every tag in
		// it was already handed over.
		assertTag(t, byStop[1], "alpha", true, "the retry re-sends the reply the refused Stop was shown")
		assertTag(t, byStop[1], "beta", true, "the retry has no text of its own, so #beta was shown before too")
		assertFile(t, byStop[1], "notes.md", true, "notes.md is unchanged since the refused Stop")
		return
	}
	if len(byStop[0]) == 0 || len(byStop[1]) == 0 {
		t.Fatalf("expected events at the refused Stop (0) and the retry (1), got stops %v", keys(byStop))
	}

	// The refused Stop: everything is new.
	assertTag(t, byStop[0], "alpha", false, "at the first Stop nothing was shown to an earlier one")
	assertTag(t, byStop[0], "beta", false, "at the first Stop nothing was shown to an earlier one")
	assertFile(t, byStop[0], "notes.md", false, "at the first Stop nothing was shown to an earlier one")

	// The retry: #alpha and notes.md are re-sent; #beta was written again.
	assertTag(t, byStop[1], "alpha", true,
		"#alpha is only in the refused reply's text, re-sent with the retry — it must be marked seen")
	assertTag(t, byStop[1], "beta", false,
		"#beta is in the retry's own text — a tag the agent wrote again is not a re-send")
	assertFile(t, byStop[1], "notes.md", true,
		"notes.md is unchanged since the refused Stop was handed it — a re-send")
}

func keys(m map[int][]map[string]any) []int {
	var out []int
	for k := range m {
		out = append(out, k)
	}
	return out
}

func assertTag(t *testing.T, evs []map[string]any, label string, wantSeen bool, why string) {
	t.Helper()
	for _, ev := range evs {
		if ev["kind"] != "PostTagWrite" {
			continue
		}
		tags, _ := ev["tags"].([]any)
		for _, raw := range tags {
			tag, _ := raw.(map[string]any)
			if tag["label"] != label {
				continue
			}
			seen, ok := tag["seen"].(bool)
			if !ok {
				t.Errorf("#%s carries no boolean seen: %v", label, tag)
				return
			}
			if seen != wantSeen {
				t.Errorf("#%s seen=%v, want %v: %s\nevents: %v", label, seen, wantSeen, why, evs)
			}
			return
		}
	}
	t.Errorf("no PostTagWrite carried #%s\nevents: %v", label, evs)
}

func assertFile(t *testing.T, evs []map[string]any, path string, wantSeen bool, why string) {
	t.Helper()
	for _, ev := range evs {
		kind, _ := ev["kind"].(string)
		if !strings.HasPrefix(kind, "PostFile") || ev["path"] != path {
			continue
		}
		seen, ok := ev["seen"].(bool)
		if !ok {
			t.Errorf("%s event for %s carries no boolean seen: %v", kind, path, ev)
			return
		}
		if seen != wantSeen {
			t.Errorf("%s seen=%v, want %v: %s", path, seen, wantSeen, why)
		}
		return
	}
	t.Errorf("no Post file event for %s\nevents: %v", path, evs)
}
