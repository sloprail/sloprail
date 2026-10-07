package e2e

// TODO(D3): drive verdict via a10n-claude-mock once a10n-cli#470 lands + new mock
// on PATH; today InstallJudgeClaude supplies the verdict.
//
// Use case: marker-anchored-structure. A file-guard bound to any file carrying an
// `sr:endpoint` marker — the marker anchors the rule, not the path. Two checks:
//
//   - SCRIPT (file-name-matches-pattern.sh): the file's own name must follow the
//     endpoint naming pattern `<verb>-<resource>.<ext>` (e.g. get-users.ts). Pure
//     deterministic check on the event's path — no model.
//   - JUDGE (endpoint-uses-required-libs.md.j2): does the marked code use the
//     project's required stack (Express + Prisma, kebab-case routes, camelCase
//     fields)?
//
// The guard is a plain file-guard, so refusals arrive at Stop, read with
// BlockingErrorsFrom(proj, sess, "Stop"); res.Refused() stays false.
//
// How each mechanism is driven:
//   - MARKER: written in the file's own content as a `// sr:endpoint <fqn>` line,
//     so filemod.Scan lifts it onto newMarkers and the guard's
//     `match: any(markers, .kind == "endpoint")` selects the file.
//   - SCRIPT: the filename is chosen per test — a good `<verb>-<resource>.<ext>`
//     name vs a bad one — giving the script a genuinely different, un-stubbed
//     input independent of the judge.
//   - JUDGE verdict: InstallJudgeClaude supplies the model's pass/fail.
//
// This example ships its one script (file-name-matches-pattern.sh) executable —
// no exec-bit bug like business-invariants / grounding-citations had.

import "testing"

// masProject stands up a project with the marker-anchored-structure example
// installed. The example ships executable, so no chmod is needed.
func masProject(t *testing.T, e *env) string {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, "marker-anchored-structure")
	return proj
}

// T048_05: DOES NOT FIRE OUTSIDE ITS MATCH — a file carrying a DIFFERENT marker
// kind (sr:doc, not sr:endpoint) with a bad name is not selected. The guard binds
// specifically to `sr:endpoint`; a marker of another kind does not arm it. This
// distinguishes "any marker" from "the endpoint marker".
func TestT048_05_DifferentMarkerKindDoesNotFire(t *testing.T) {
	e := newEnv(t)
	proj := masProject(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "must not be reached — wrong marker kind"}`)

	sess := "s-048-05"
	res := e.Run(proj, sess, "add a doc-marked file with a bad name", Turns("done",
		Write("w1", "GetUsers.ts", "// sr:doc some.thing\nconst x = 1\n"), // sr:doc, not sr:endpoint
	).ThenCommit("write the files"))

	if res.Refused() {
		t.Fatalf("a file with a non-endpoint marker was refused at pre-tool — the guard overreached:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a file carrying only an sr:doc marker blocked — the endpoint guard matched the wrong kind:\n%s",
			joinBlocks(blocks))
	}
}
