package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Say lets a scenario write prose carrying a #tag, which the Stop dispatch scans
// into a PostTagWrite — the trigger the research-rigor composite's context enters
// on. This file proves a context enters on a tag it recognises, at Stop, the same
// as it enters on a Post file write.
var Say = harness.Say

// researchContext enters on a PostTagWrite carrying #research — the research-rigor
// pattern (a context recognising a declared research branch from the tag the
// engine already found, no trajectory grep).
const researchContext = `on:
  - event: PostTagWrite
    match: any(event.tags, .label == "research")
enter: ./enter.sh
exit: ./exit.sh
`

// enterOnResearch activates carrying a fixed marker so a test can see it entered
// on the tag.
const enterOnResearch = `#!/bin/sh
cat >/dev/null
printf '{"declared":"research"}'
exit 0
`

// T035_11: a context enters on a PostTagWrite it recognises.
//
// The agent writes "#research" in its prose; at Stop the tag module scans it into
// a PostTagWrite, the context's trigger match (any(event.tags, .label ==
// "research")) admits it, and enter runs — the context activates. Proves the
// context lifecycle reaches the tag trigger, not only file triggers.
func TestT035_11_ContextEntersOnTag(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "research-run", researchContext, map[string]string{
		"enter.sh": enterOnResearch,
		"exit.sh":  exitNever,
	})
	// The tag scan reads the committed record, so the project is committed first.
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "before the session")

	sess := "s-035-11"
	e.Run(proj, sess, "declare research", Turns("done",
		Say("m1", "I'll dig into this as a #research task."),
	))

	active, payload := e.ContextState(proj, sess, "research-run")
	if !active {
		t.Fatalf("the context did not enter on the #research tag")
	}
	if payload["declared"] != "research" {
		t.Errorf("the context entered but its payload was not enter's: %v", payload)
	}
}

// T035_12: the SAME context does NOT enter on a DIFFERENT tag — the trigger's
// match narrows to #research.
//
// The control for T035_11: the agent writes #update, not #research, so the
// trigger's match excludes it and the context stays inactive.
func TestT035_12_ContextDoesNotEnterOnOtherTag(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "research-run", researchContext, map[string]string{
		"enter.sh": enterOnResearch,
		"exit.sh":  exitNever,
	})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "before the session")

	sess := "s-035-12"
	e.Run(proj, sess, "tag something else", Turns("done",
		Say("m1", "Noting this as #update only."),
	))

	if active, _ := e.ContextState(proj, sess, "research-run"); active {
		t.Errorf("the context entered on a tag its trigger's match should have excluded")
	}
}
