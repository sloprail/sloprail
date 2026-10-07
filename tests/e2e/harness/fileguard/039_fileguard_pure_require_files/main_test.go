package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// pure_require_files: a `require: [{skill, files}]` prerequisite — naming pages
// INSIDE a skill that must ALSO have been read, beyond the skill's own SKILL.md.
//
// This closes a gap `require: [{skill}]` alone leaves open: loading a skill only
// guarantees its SKILL.md was read, and a skill's real detail — a per-event-kind
// script skeleton, a judge contract — often lives in a page SKILL.md merely LINKS
// to. Measured against a real onboarding run: every agent loaded
// authoring-guardrails, yet none of them opened file-guard.md (where the correct
// script skeleton lives), and every check script was written from memory and
// refused several times before it was right. `files` lets a rule require that a
// SPECIFIC subpage was read, not merely the skill's own front page.
//
// This suite mirrors 037_fileguard_pure_require's own pair one field over: same
// skill/loaded-vs-not shape, extended with the files half — the skill loaded but
// a required subpage NOT read still refuses, and reading that subpage too
// permits the write.
var New = harness.New

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

var (
	Turns   = harness.Turns
	Write   = harness.Write
	Skill   = harness.Skill
	Bash    = harness.Bash
	ToolUse = harness.ToolUse
)

// writeProjectSkill writes a project skill's SKILL.md, and a subpage beside it,
// at the exact layout SkillFilePaths/SkillSubpagePaths resolve
// (`<proj>/.claude/skills/<name>/…`). Returns the subpage's absolute path, which
// is what a Read turn or a `cat` Bash turn must name to satisfy a `files` entry.
func writeProjectSkill(t *testing.T, proj, name, subpage string) string {
	t.Helper()
	dir := filepath.Join(proj, filepath.FromSlash(harness.ProjectSkillDir(t)), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\nbody, see "+subpage+"\n"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	subpagePath := filepath.Join(dir, subpage)
	if err := os.WriteFile(subpagePath, []byte("subpage detail\n"), 0o644); err != nil {
		t.Fatalf("write subpage %s: %v", subpage, err)
	}
	return subpagePath
}
