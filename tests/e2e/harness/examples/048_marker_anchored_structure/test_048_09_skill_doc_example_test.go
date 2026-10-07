package e2e

import "testing"

// T048_09: a doc example of the marker inside a skill is not an endpoint. The
// scanner reads `// sr:endpoint "<verb>-<resource>"` anywhere in a file's text, so
// a SKILL.md that teaches the marker used to be treated as an endpoint file and
// refused. The rule is about code; a file under .claude/ is out of its match. The
// control, a real code file carrying the same marker, is still refused.
func TestT048_09_SkillDocExampleIsNotAnEndpoint(t *testing.T) {
	e := newEnv(t)
	proj := masProject(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "not a conforming endpoint"}`)

	marker := "// sr:endpoint \"<verb>-<resource>\"\n"

	// The doc example inside a skill is admitted (first, so a refusal here is its own: a
	// refused range is replayed until the commits change).
	e.Run(proj, "s-048-09b", "document the marker", Turns("done",
		Write("w1", ".claude/skills/mark-endpoints/SKILL.md",
			"---\nname: mark-endpoints\ndescription: mark endpoints\n---\n\nWrite:\n\n    "+marker),
	).ThenCommit("document the marker"))
	if blocks := e.BlockingErrorsFrom(proj, "s-048-09b", "Stop"); len(blocks) != 0 {
		t.Fatalf("a SKILL.md doc example of the marker was judged as an endpoint:\n%s", joinBlocks(blocks))
	}

	// Control: real code with the marker is refused (the judge fails it).
	e.Run(proj, "s-048-09a", "add an endpoint", Turns("done",
		Write("w1", "get-users.ts", marker+"const x = 1\n"),
	).ThenCommit("write the code"))
	if blocks := e.BlockingErrorsFrom(proj, "s-048-09a", "Stop"); len(blocks) == 0 {
		t.Fatalf("a real endpoint file with a failing judge was not refused")
	}
}
