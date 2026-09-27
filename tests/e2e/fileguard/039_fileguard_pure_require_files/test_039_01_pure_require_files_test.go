package e2e

import (
	"strings"
	"testing"
)

// pureRequireFilesGuard is a PURE-require file-guard, like 037's own, but naming a
// `files` entry alongside the skill: a write under memories/topics/ is permitted
// only once BOTH the document-topic skill was loaded AND its own "detail.md"
// subpage was read this session. Preventive, so the write never lands while the
// precondition is unmet.
const pureRequireFilesGuard = `match: "memories/topics/**/*.md"
preventive: true
require:
  - skill: document-topic
    files: [detail.md]
`

// T039_01: the guard VALIDATES — `sr-file declarations` accepts a file-guard whose
// require carries a `files` entry alongside `skill`.
func TestT039_01_PureRequireFilesGuardValidates(t *testing.T) {
	e := New(t)
	proj := e.Project()
	writeProjectSkill(t, proj, "document-topic", "detail.md")
	e.FileGuard(proj, "require-topic-detail", pureRequireFilesGuard, nil)

	res := e.CLIDirect(proj, "sr-file", "declarations", proj)
	if res.Code != 0 {
		t.Fatalf("a require-with-files file-guard must load clean, got exit %d:\n%s", res.Code, res.Output)
	}
	if !strings.Contains(res.Output, "file-guards (1): require-topic-detail") {
		t.Errorf("the loaded report did not name the guard:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "could not be loaded") {
		t.Errorf("the require-with-files guard was reported invalid — it must load:\n%s", res.Output)
	}
}

// T039_02: skill loaded, files NOT read — still refused.
//
// The skill's own Skill tool_use is in the record (so the bare `{skill}` half is
// satisfied) but the required subpage was never read, so the `files` half is not
// — and the whole prerequisite must still refuse. This is the property `files`
// exists for: without it, loading the skill alone would have been enough.
func TestT039_02_SkillLoadedButFileUnreadStillRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	writeProjectSkill(t, proj, "document-topic", "detail.md")
	e.FileGuard(proj, "require-topic-detail", pureRequireFilesGuard, nil)
	commitGuards(t, proj)

	res := e.Run(proj, "s-039-02", "load the skill but not its detail page, then write a topic", Turns("done",
		Skill("s1", "document-topic"),
		Write("w1", "memories/topics/idea.md", "# an idea"),
	))

	if !res.Refused() {
		t.Fatalf("the write landed though the required subpage was never read:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/topics/idea.md") {
		t.Errorf("the write LANDED despite the files require being unmet")
	}
	if !res.Saw("detail.md") {
		t.Errorf("the refusal did not name the missing subpage:\n%s", res.Output)
	}
}

// T039_03: neither skill nor files — refused, naming the skill (not the file):
// the skill itself takes priority when it was never loaded at all.
func TestT039_03_NeitherSkillNorFileRefusesNamingTheSkillFirst(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	writeProjectSkill(t, proj, "document-topic", "detail.md")
	e.FileGuard(proj, "require-topic-detail", pureRequireFilesGuard, nil)
	commitGuards(t, proj)

	res := e.Run(proj, "s-039-03", "write a topic with neither skill nor detail read", Turns("done",
		Write("w1", "memories/topics/idea.md", "# an idea"),
	))

	if !res.Refused() {
		t.Fatalf("the write landed though neither the skill nor its subpage was ever read:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/topics/idea.md") {
		t.Errorf("the write LANDED despite the require being wholly unmet")
	}
	if !res.Saw("document-topic") {
		t.Errorf("the refusal did not name the skill:\n%s", res.Output)
	}
}

// T039_04: BOTH skill loaded AND the required subpage read — the write PERMITS.
//
// The subpage is read with a Read tool_use naming its own resolved path, which is
// exactly the same evidence a bare `{skill}` already accepts for a skill's own
// SKILL.md, extended here to the subpage `files` names.
func TestT039_04_SkillLoadedAndFileReadPermitsWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	subpagePath := writeProjectSkill(t, proj, "document-topic", "detail.md")
	e.FileGuard(proj, "require-topic-detail", pureRequireFilesGuard, nil)
	commitGuards(t, proj)

	res := e.Run(proj, "s-039-04", "load the skill, read its detail page, then write a topic", Turns("done",
		Skill("s1", "document-topic"),
		ToolUse("r1", "Read", map[string]string{"file_path": subpagePath}),
		Write("w1", "memories/topics/idea.md", "# an idea"),
	))

	if res.Refused() {
		t.Fatalf("the write was refused though both the skill and its required subpage were read:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/topics/idea.md") {
		t.Errorf("the write did not land though the require was fully met")
	}
}

// T039_05: the Bash half — the subpage read as a `cat`, not a Read tool_use, still
// satisfies `files`. Mirrors 037's own Bash coverage of the bare `{skill}` case
// (require_read_test.go's TestSkillLoadedInTrajectory_CatOnSkillFileCounts).
func TestT039_05_CatOnTheSubpagePermitsWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	subpagePath := writeProjectSkill(t, proj, "document-topic", "detail.md")
	e.FileGuard(proj, "require-topic-detail", pureRequireFilesGuard, nil)
	commitGuards(t, proj)

	res := e.Run(proj, "s-039-05", "load the skill, cat its detail page, then write a topic", Turns("done",
		Skill("s1", "document-topic"),
		Bash("b1", "cat "+subpagePath),
		Write("w1", "memories/topics/idea.md", "# an idea"),
	))

	if res.Refused() {
		t.Fatalf("the write was refused though the subpage was `cat`, which counts the same as a Read:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/topics/idea.md") {
		t.Errorf("the write did not land though the require was fully met")
	}
}
