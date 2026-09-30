package e2e

import (
	"path/filepath"
	"testing"
)

// unit-md-first is a PreFileWrite gate (refuses before the write lands) plus a file-guard (over the committed tree at Stop), script only, path-based: any file
// under memories/topics/<topic>/units/<unit>/ other than UNIT.md may only be
// written once that unit's own UNIT.md exists on disk. Without it, a folder's
// UNIT.md-less files are orphaned — unit-satisfies-rules and
// unit-publish-approved both key off UNIT.md's frontmatter.
//
// Proven first as a project-local rule in the strategy repo's own
// .sloprail/file-guard/unit-md-first/ (check.sh, unchanged here) before being
// generalized into this plugin.
//
// unitDir is the unit folder every scenario below writes into:
// memories/topics/20260101_launch/units/01_announce (unitPath/draftPath's
// parent, both already defined in main_test.go).
var unitDir = filepath.Dir(unitPath)

// installUnitMdFirstProject stands up a project with the plugin installed and
// a PASS judge stub — unit-satisfies-rules's Stop after-check judges every
// settled UNIT.md/02_draft.md write even with no rule configured, so any
// scenario that lands one of those two paths needs the stub even though this
// test file is not about that guard.
func installUnitMdFirstProject(t *testing.T) (*Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	return e, proj
}

// TestUnitMdFirst_NonEntryFileBeforeUnitMdRefused: writing the unit's draft
// file into a unit folder with no UNIT.md yet is refused before it lands, and
// the refusal names the missing UNIT.md and says to write it first.
func TestUnitMdFirst_NonEntryFileBeforeUnitMdRefused(t *testing.T) {
	e, proj := installUnitMdFirstProject(t)

	res := e.Run(proj, "s-unitmd-before", authPrompt, Turns("done",
		Write("w1", draftPath, "# Announce\n\nDraft body.\n"),
	))
	if !res.Refused() {
		t.Fatalf("a draft written before UNIT.md existed was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, draftPath) {
		t.Errorf("the gate let a draft land before UNIT.md existed")
	}
	if !res.Saw(unitDir+" has no UNIT.md") || !res.Saw("Write "+unitDir+"/UNIT.md first") {
		t.Errorf("the refusal does not name the missing UNIT.md and how to fix it:\n%s", res.Output)
	}
}

// TestUnitMdFirst_UnitMdItselfAdmitted: writing UNIT.md itself into a unit
// folder that has none yet is admitted — the guard's match excludes UNIT.md,
// so nothing here blocks the file that satisfies the rule for every other
// file in the folder.
func TestUnitMdFirst_UnitMdItselfAdmitted(t *testing.T) {
	e, proj := installUnitMdFirstProject(t)

	res := e.Run(proj, "s-unitmd-itself", authPrompt, Turns("done",
		Write("w1", unitPath, draftingUnit),
	).ThenCommit("Add the unit"))
	if res.Refused() {
		t.Fatalf("writing UNIT.md itself was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, unitPath) {
		t.Errorf("an admitted UNIT.md write did not land on disk")
	}
}

// TestUnitMdFirst_NonEntryFileAfterUnitMdAdmitted: UNIT.md is written first,
// then the draft — in the SAME run, so the pre-write check for the second
// write sees UNIT.md already on disk. Both land.
func TestUnitMdFirst_NonEntryFileAfterUnitMdAdmitted(t *testing.T) {
	e, proj := installUnitMdFirstProject(t)

	res := e.Run(proj, "s-unitmd-after", authPrompt, Turns("done",
		Write("w1", unitPath, draftingUnit),
		Write("w2", draftPath, "# Announce\n\nDraft body.\n"),
	).ThenCommit("Add the unit and its draft"))
	if res.Refused() {
		t.Fatalf("a draft written after UNIT.md existed was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, unitPath) || !e.Exists(proj, draftPath) {
		t.Errorf("an admitted UNIT.md + draft pair did not both land on disk")
	}
}

// TestUnitMdFirst_DeletionNotRefused: a unit folder already on disk (UNIT.md
// and its draft, seeded into the session baseline) has its draft file
// deleted. Neither the gate nor the file-guard covers deletions, so neither is
// even asked about the delete, and it is not refused.
func TestUnitMdFirst_DeletionNotRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, unitPath, draftingUnit)
	e.WriteFile(proj, draftPath, "# Announce\n\nDraft body.\n")
	// Seeds are history before the rules exist (a rule's range starts at the parent
	// of the commit that installs it).
	e.CommitAll(proj, "the seeded rules and unit")
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-unitmd-delete", authPrompt, Turns("done",
		Bash("b1", "rm "+draftPath),
	).ThenCommit("Remove the draft"))
	if res.Refused() {
		t.Fatalf("deleting a unit's non-entry file was refused:\n%s", res.Output)
	}
	if e.Exists(proj, draftPath) {
		t.Errorf("the permitted delete did not remove the file")
	}
	if !e.Exists(proj, unitPath) {
		t.Errorf("the sibling UNIT.md was unexpectedly removed too")
	}
}
