package dispatch

import (
	"os"
	"path/filepath"

	"github.com/sloprail/sloprail/internal/harness"
)

// This file resolves a skill NAME to the SKILL.md file(s) it could live at, on
// disk, from the perspective of a session running against a given workspace —
// and, via SkillSubpagePaths, the same for any OTHER file inside that skill's
// own directory, for a `require: [{skill, files}]` prerequisite naming a
// subpage.
//
// It exists for one caller: checkSkill's read-detection half (see require.go),
// which must know WHICH FILE to look for a Read tool_use or a `cat`-shaped Bash
// command against, given only the name a `require: [{skill: document-decision}]`
// prerequisite declares. A Skill tool_use already carries the name alone —
// Claude Code resolves it internally — but a Read or a Bash command names a
// PATH, so the two have to be connected here.
//
// # The candidate locations, and why there is more than one
//
// A skill a project can load comes from exactly the two places anything else a
// project loads from can come from, and this mirrors internal/harness's own
// stance on that split (see claudecode.go's package comment):
//
//  1. The project's OWN skills, at `<workspace>/.claude/skills/<name>/SKILL.md`
//     — the same relative layout this repo's own `.claude/skills/` uses.
//  2. A plugin's shipped skills, at `<pluginRoot>/skills/<name>/SKILL.md` for
//     every plugin the project has enabled — the same pluginRoot
//     internal/harness.Resolve already locates for declaration.NewWithPlugins
//     to read a plugin's `.sloprail/`. A plugin's skills/ sits beside its
//     .sloprail/ at the plugin's own root (see declaration/store.go's own
//     comment: "a plugin's `.sloprail/`, `hooks/`, `skills/`").
//
// Reusing harness.Resolve rather than a second discovery mechanism is the
// whole point: which plugins are enabled is a fact about the project's Claude
// Code settings, already read once per dispatch for the plugin-shipped
// declarations themselves (services/sr-session's natureDeclarationStore). A
// second, independent answer to "which plugins are enabled" would be a second
// place for that answer to drift from the first.
//
// # What is NOT resolved here
//
// This does not resolve a skill shipped by a plugin that is not ENABLED for
// this project — the same reach harness.Resolve itself has, and for the same
// reason: an unresolved plugin already has its own reporting path
// (harness.Unresolved), and a skill precondition silently trusting a
// not-installed plugin's file would be answering a different question than
// "was the skill this project actually has loaded".
//
// # Candidates, not a single answer
//
// Every candidate is returned, in resolution order, rather than the first one
// that exists — SkillFilePaths itself does no stat call. The caller (checkSkill)
// is the one deciding "was ANY of these read", and stat'ing here would mean
// this file makes a filesystem judgement that belongs one level up, the same
// separation commandmod draws between naming a path and deciding what is at it.

// SkillFilePaths returns every path a skill named `name` could be loaded from,
// in resolution order, given the project workspace it is running against.
//
// workspace is the tree's own root — Request.Workspace, the git root a session
// is anchored to, which is exactly the projectDir harness.Resolve itself takes
// (see services/sr-session's natureDeclarationStore, which resolves plugins
// from the identical anchor). An empty workspace resolves nothing: without a
// project root there is no `.claude/` to look under and no settings file to
// read plugins from, so the honest answer is no candidates rather than a guess
// rooted at the process's own working directory.
//
// A home directory that cannot be located, or a settings file that cannot be
// read, degrades to "no plugin candidates" rather than an error — the same
// fail-open natureDeclarationStore itself applies to the identical failure,
// because a plugin skill this cannot find is not a reason to also stop looking
// for the project's OWN skill file, which needs no plugin resolution at all.
func SkillFilePaths(workspace, name string) []string {
	return skillCandidatePaths(workspace, name, "SKILL.md")
}

// SkillSubpagePaths returns every path a FILE INSIDE skill `name` — file,
// relative to the skill's own directory (the one holding its SKILL.md), e.g.
// "script-checks.md" — could live at, in the same resolution order
// SkillFilePaths uses for the skill itself.
//
// This is the `require: [{skill, files}]` prerequisite's read-detection half
// (see require.go): a `files` entry names a page relative to the skill, and
// this connects that name to the concrete path(s) a Read tool_use or a
// file-reading Bash command would name — the identical join SkillFilePaths
// itself does with the one hardcoded name "SKILL.md".
func SkillSubpagePaths(workspace, name, file string) []string {
	return skillCandidatePaths(workspace, name, file)
}

// skillCandidatePaths is the shared walk both SkillFilePaths and
// SkillSubpagePaths run: every directory a skill named `name` could live in,
// each joined with rel (its own SKILL.md, or a subpage relative to it).
func skillCandidatePaths(workspace, name, rel string) []string {
	if workspace == "" || name == "" || rel == "" {
		return nil
	}

	paths := []string{
		filepath.Join(workspace, ".claude", "skills", name, rel),
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return paths
	}
	res, err := harness.Resolve(workspace, home)
	if err != nil {
		return paths
	}
	for _, root := range res.Roots {
		paths = append(paths, filepath.Join(root.Dir, "skills", name, rel))
	}
	return paths
}
