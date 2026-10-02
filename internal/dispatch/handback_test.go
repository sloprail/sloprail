package dispatch

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/grounding"
)

// A sub-agent refused at the changeset for want of the user's words is told to
// hand the change back — a patch file, a revert, a report to its parent — and
// never to ask the user or to leave it for a merge trailer; the root keeps the
// plain instruction.
func TestCitationRefusalForASubagentHandsTheChangeBack(t *testing.T) {
	userOnly := declaration.Prerequisite{Citation: &declaration.CitationPrerequisite{SourceTypes: []string{"user"}}}
	cs := &changeset.Payload{Changeset: changeset.Changeset{Base: "abc123", Files: []changeset.File{{Path: "docs/a.md"}, {Path: "docs/b c.md"}}}}
	ask := func(sub bool) string {
		v, err := Runner{}.Run(Request{Nature: NatureFileGuard, Subagent: sub, Changeset: cs,
			Require: []declaration.Prerequisite{userOnly},
			Event:   event.Event{Kind: changeset.Kind, Fields: map[string]any{grounding.FieldCitations: grounding.ToWire(nil)}}})
		require.NoError(t, err)
		require.True(t, v.Refused)
		return v.Reason
	}
	got := ask(true)
	for _, want := range []string{
		"you cannot get the user's words yourself",
		"git diff --binary abc123..HEAD -- docs/a.md 'docs/b c.md' > ",
		"git apply -R --index ", "git commit -m",
		"AskUserQuestion", "Sloprail-Cites-User: <the user's exact answer>", "EXACTLY what needs the user's approval",
	} {
		assert.Contains(t, got, want)
	}
	assert.NotContains(t, strings.ReplaceAll(got, "never a branch, tag or stash", ""), "stash")
	assert.NotContains(t, got, "backup/")
	root := ask(false)
	assert.Contains(t, root, "Amend or add a commit in this range")
	assert.NotContains(t, root, "hand the change back")
}
