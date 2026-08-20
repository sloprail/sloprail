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
// The guard is NOT preventive, so refusals arrive at Stop, read with
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

// endpoint is a conforming Express + Prisma endpoint body carrying the marker.
const conformingEndpoint = "// sr:endpoint users.list\n" +
	"import express from 'express'\n" +
	"const router = express.Router()\n" +
	"router.get('/user-profiles', async (req, res) => {\n" +
	"  const rows = await prisma.user.findMany()\n" +
	"  res.json({ userId: rows[0].id })\n" +
	"})\n"

// T048_01: HAPPY PATH — a well-named endpoint file whose code uses the required
// stack. The script passes (good name) and the judge passes (Express + Prisma),
// so the write is admitted.
func TestT048_01_GoodNameConformingCodeAdmits(t *testing.T) {
	e := newEnv(t)
	proj := masProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "Express Router + prisma, kebab-case route, camelCase fields"}`)

	sess := "s-048-01"
	res := e.Run(proj, sess, "add a conforming endpoint", Turns("done",
		Write("w1", "get-users.ts", conformingEndpoint),
	))

	if res.Refused() {
		t.Fatalf("a well-named conforming endpoint was refused at pre-tool:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a well-named conforming endpoint blocked at Stop:\n%s", joinBlocks(blocks))
	}
}

// T048_02: SCRIPT REFUSAL — a BADLY-NAMED endpoint file. The name does not follow
// `<verb>-<resource>.<ext>`, so the SCRIPT refuses before the judge is asked, and
// its naming reason reaches the agent.
//
// The judge is stubbed to PASS, so a block can only be the script's. The file
// still carries a valid sr:endpoint marker, so the guard DOES select it — only
// the name is wrong.
func TestT048_02_BadFileNameBlocksViaScript(t *testing.T) {
	e := newEnv(t)
	proj := masProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the script should refuse first"}`)

	sess := "s-048-02"
	e.Run(proj, sess, "add a badly-named endpoint", Turns("done",
		// CamelCase, no verb-resource hyphen — violates the naming pattern.
		Write("w1", "GetUsers.ts", conformingEndpoint),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a badly-named endpoint file was not refused by the script")
	}
	joined := joinBlocks(blocks)
	if !containsAll(joined, "does not follow the <verb>-<resource>.<ext> naming pattern", "endpoint-conforms") {
		t.Fatalf("the bad-name (script) reason did not reach the agent:\n%s", joined)
	}
}

// T048_03: JUDGE REFUSAL — a well-named endpoint whose code uses a FORBIDDEN
// stack (raw `pg`, not Prisma). The script passes (good name) but the judge
// refuses, and its reasoning reaches the agent.
//
// The code genuinely uses the wrong library, so the judge has a real input — not
// just the stub flipping.
func TestT048_03_NonConformingStackBlocksViaJudge(t *testing.T) {
	e := newEnv(t)
	proj := masProject(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "uses new Pool() from pg where prisma.user is required"}`)

	nonConforming := "// sr:endpoint users.list\n" +
		"const { Pool } = require('pg')\n" +
		"const pool = new Pool()\n" +
		"app.get('/userProfiles', async (req, res) => { await pool.query('select 1') })\n"

	sess := "s-048-03"
	e.Run(proj, sess, "add a non-conforming endpoint", Turns("done",
		Write("w1", "get-users.ts", nonConforming),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("an endpoint using a forbidden stack was not refused by the judge")
	}
	joined := joinBlocks(blocks)
	if !containsAll(joined, "where prisma.user is required", "endpoint-conforms") {
		t.Fatalf("the judge's reasoning did not reach the agent:\n%s", joined)
	}
}

// T048_04: THE MARKER ANCHORS THE RULE. A file with a BAD name but NO sr:endpoint
// marker is not selected at all — even though the name would fail the script and
// the judge is stubbed to FAIL, nothing blocks, because the guard never fires.
// Same bad name as T048_02; the ONLY difference is the absent marker, so this
// isolates the marker as what arms the rule.
func TestT048_04_NoMarkerDoesNotFire(t *testing.T) {
	e := newEnv(t)
	proj := masProject(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "must not be reached — no sr:endpoint marker"}`)

	sess := "s-048-04"
	res := e.Run(proj, sess, "add a badly-named file with no marker", Turns("done",
		Write("w1", "GetUsers.ts", "const x = 1\nconst y = 2\n"), // bad name, NO marker
	))

	if res.Refused() {
		t.Fatalf("an unmarked file was refused at pre-tool — the guard fired without a marker:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("an unmarked, badly-named file blocked — the guard is not marker-anchored:\n%s",
			joinBlocks(blocks))
	}
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
	))

	if res.Refused() {
		t.Fatalf("a file with a non-endpoint marker was refused at pre-tool — the guard overreached:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a file carrying only an sr:doc marker blocked — the endpoint guard matched the wrong kind:\n%s",
			joinBlocks(blocks))
	}
}
