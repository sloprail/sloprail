package e2e

import (
	"testing"
)

// This file covers a tag written inside markdown EMPHASIS (issue #89). A real
// eval agent closed its research with `**#research summary:**`; the tag parser
// read `#` right after `**` as mid-token, found no tag, and the research-run
// context never activated — so no rule that depends on it ran. Emphasis around a
// tag is the agent saying it loudly: it is the tag. Text the agent is SHOWING (a
// code span, a fenced block, a quoted line) is still not, emphasised or not.

// T035_13: a context enters on its tag written in bold, italic, underscore or
// mixed emphasis — each form in its own session, so each is proved on its own.
//
// On origin/main none of these activated the context.
func TestT035_13_ContextEntersOnEmphasisedTag(t *testing.T) {
	for i, msg := range []string{
		"Done. **#research summary:** backoff with jitter.",
		"Starting a *#research* pass.",
		"Starting a _#research_ pass.",
		"Starting a __#research__ pass.",
		"***#research*** first.",
		"**_#research_** first.",
	} {
		t.Run(msg, func(t *testing.T) {
			e := New(t)
			proj := e.Project()
			e.GitInit(proj)
			e.Context(proj, "research-run", researchContext, map[string]string{
				"enter.sh": enterOnResearch,
				"exit.sh":  exitNever,
			})
			e.Git(proj, "add", "-A")
			e.Git(proj, "commit", "-m", "before the session")

			sess := "s-035-13-" + string(rune('a'+i))
			e.Run(proj, sess, "declare research", Turns("done",
				Say("m1", msg),
			))

			active, payload := e.ContextState(proj, sess, "research-run")
			if !active {
				t.Fatalf("the context did not enter on the tag in %q", msg)
			}
			if payload["declared"] != "research" {
				t.Errorf("the context entered but its payload was not enter's: %v", payload)
			}
		})
	}
}

// T035_14: an emphasised tag the agent is SHOWING does not enter the context —
// inside a code span, a fenced block, a quoted line — and neither does emphasis
// that is not around a tag at the word boundary (intraword, struck through).
// The control for T035_13: emphasis widened what counts as SAID, not what counts
// as shown.
func TestT035_14_EmphasisedTagShownOrIntrawordDoesNotEnter(t *testing.T) {
	for i, msg := range []string{
		"The declaration is `**#research**`, as an example.",
		"An example reply:\n```\n**#research summary:** ...\n```\nThat is the shape.",
		"> **#research summary:** quoted from the notes\nI read the above.",
		"see notes**#research** here",
		"~~#research~~ not any more",
	} {
		t.Run(msg, func(t *testing.T) {
			e := New(t)
			proj := e.Project()
			e.GitInit(proj)
			e.Context(proj, "research-run", researchContext, map[string]string{
				"enter.sh": enterOnResearch,
				"exit.sh":  exitNever,
			})
			e.Git(proj, "add", "-A")
			e.Git(proj, "commit", "-m", "before the session")

			sess := "s-035-14-" + string(rune('a'+i))
			e.Run(proj, sess, "talk about research", Turns("done",
				Say("m1", msg),
			))

			if active, _ := e.ContextState(proj, sess, "research-run"); active {
				t.Errorf("the context entered on a tag the agent only showed, in %q", msg)
			}
		})
	}
}
