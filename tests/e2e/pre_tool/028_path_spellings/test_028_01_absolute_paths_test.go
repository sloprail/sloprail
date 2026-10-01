// Package e2e covers the spellings a path can arrive in, and what a rule is
// therefore able to narrow on.
//
// This is hook_within_binding at its most consequential. A match is a prefix
// test over the reported subject, so the spelling the engine chooses IS the
// scope of every narrowed rule in the project. Get it wrong in one direction and
// `path startsWith "secret/"` stops admitting the writes it was written for; get
// it wrong in the other and a path outside the repository is handed to a rule as
// though it were inside.
//
// 018 covers awkward CHARACTERS in a path — spaces, unicode, quotes, newlines —
// and every path it uses is relative. The spellings here are the ones a real
// session produces and no test exercised:
//
//   - ABSOLUTE. This is what Claude Code actually sends. Its Write tool reports
//     `file_path` as an absolute path, so if a rule written as
//     `path startsWith "docs/"` did not admit `/Users/x/repo/docs/a.md`, every
//     narrowed rule in every real project would silently never fire. That makes
//     it the single most load-bearing case in this file, and it had no test.
//   - OUTSIDE the repository, which must NOT be pulled in.
//   - Reached through a SYMLINK that escapes the repository, which must not be
//     pulled in either — and which a lexical containment check gets wrong.
//   - The same directory spelled two ways (/tmp vs /private/tmp on macOS),
//     which must be recognised as one.
//
// Every assertion is on the spelling the check was HANDED, read off the payload,
// because that is what a match sees. A test asserting only that the write was
// permitted or refused would pass on an engine reporting any spelling at all.
//
// Path canonicalization (`reportable`) is shared engine machinery the new dispatch
// still uses: the path a file event carries is the same whichever dispatch reads
// it. The observer is a gate triggering on the pre-write events, recording
// `.event.path` FLAT (see main_test.go); the narrowed rules are gates whose
// trigger match is the GateMatchScope expression `event.path startsWith "…"` — the
// analogue of the old `matcher: path startsWith "…"`, over the event's own path a
// gate reads under `event`. A gate blocks the write before it lands, so a narrowed
// rule's refusal is observed as `res.Refused()` and the file's absence, exactly as
// the old pre-tool block was. A gate rather than a file-guard because a file-guard
// does not fire for a path outside the workspace, which several of these cases
// deliberately are — see seeEveryCreate's note in main_test.go.
package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// guardsSecret is a gate that refuses any write under secret/, narrowed the
// ordinary way — `event.path startsWith "secret/"` over the GateMatchScope, the
// analogue of the old `matcher: path startsWith "secret/"` against the reported
// path. Triggering on both pre-write kinds so a create and an update are both
// caught.
const guardsSecret = `on:
  - event: PreFileCreate
    match: event.path startsWith "secret/"
  - event: PreFileUpdate
    match: event.path startsWith "secret/"
checks:
  - script: ./h.sh
`

// refuseWithReason refuses with a fixed structured reason, the new-format contract.
func refuseWithReason(reason string) string {
	return "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"" + reason + "\"}'\nexit 1\n"
}

// T028_01: an absolute path inside the workspace is reported relative to it.
//
// The case Claude Code actually produces, and the one a narrowed rule depends
// on. The agent announces `<proj>/docs/guide.md`; the rule must be handed
// `docs/guide.md`, because that is the spelling an author writes a match against.
//
// Asserted as byte equality against the relative spelling rather than as
// "contains docs/", which would pass on an engine handing over the absolute
// path unchanged — the failure this test exists for.
func TestT028_01_AnAbsolutePathInsideTheWorkspaceIsReportedRelative(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "observer", seeEveryCreate, map[string]string{"h.sh": recordPayload})

	abs := filepath.Join(proj, "docs", "guide.md")
	res := e.Run(proj, "s-028-01", "write with an absolute path", Turns("done",
		Write("w1", abs, "hello\n"),
	))
	require.False(t, res.Refused(), "a permitting rule must not refuse this")

	got := pathsSeen(t, e, proj, "observer")
	require.Len(t, got, 1, "the write must reach the rule exactly once")
	assert.Equal(t, "docs/guide.md", got[0],
		"an absolute path inside the workspace must be reported relative to it — this is the "+
			"spelling Claude Code really sends, and a match is a prefix test, so reporting it "+
			"absolute makes every narrowed rule in every real project silently never fire")
}

// T028_02: a rule narrowed the ordinary way admits a write announced absolutely.
//
// T028_01 asserts the spelling; this asserts the consequence, through a match,
// which is the thing an author actually writes. The two are separate claims: an
// engine could report the relative spelling to the check while matching against
// the absolute one, and only this test would notice.
//
// The check refuses, so the refusal reaching the agent is proof the match
// admitted the file.
func TestT028_02_ARelativeMatcherAdmitsAnAbsolutelyAnnouncedWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "guarded-dir", guardsSecret,
		map[string]string{"h.sh": refuseWithReason("this path is guarded")})

	abs := filepath.Join(proj, "secret", "keys.md")
	res := e.Run(proj, "s-028-02", "write into the guarded directory", Turns("done",
		Write("w1", abs, "hunter2\n"),
	))

	assert.True(t, res.Saw("this path is guarded"),
		"a rule written as `path startsWith \"secret/\"` must admit a write the harness "+
			"announced as an absolute path — otherwise every narrowed rule is dead in a real session")
	assert.False(t, e.Exists(proj, "secret/keys.md"), "the refused write must leave nothing behind")
}

// T028_03: the same file announced relatively and absolutely is one subject.
//
// Two spellings of one file must produce one canonical answer, or a rule's
// memory of what it has judged is keyed on how the harness happened to phrase
// the request. The engine's own comment on `reportable` names this: resolving
// returns "the ONE canonical spelling — so `a.md` and `./a.md` stop producing
// two events for one file".
//
// Three spellings of the same path, each on its own turn. All three must be
// reported identically.
func TestT028_03_SpellingsOfOneFileAgree(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "observer", seeEveryCreate, map[string]string{"h.sh": recordPayload})

	e.Run(proj, "s-028-03", "one file, three spellings", Turns("done",
		Write("w1", "docs/a.md", "one"),
		Write("w2", "./docs/b.md", "two"),
		Write("w3", filepath.Join(proj, "docs", "c.md"), "three"),
	))

	got := pathsSeen(t, e, proj, "observer")
	require.Len(t, got, 3, "every write must reach the rule")
	assert.Equal(t, []string{"docs/a.md", "docs/b.md", "docs/c.md"}, got,
		"a relative path, a dot-relative path and an absolute one must all be reported in the "+
			"one spelling a match is written against")
}

// T028_03b: a dot-relative spelling does not get past a narrowed rule.
//
// # The finding
//
// This is T028_03's consequence, and it was a live guardrail bypass rather than
// an untidy spelling. `reportable` returned a relative path verbatim, so a rule
// written `path startsWith "secret/"` was handed `./secret/keys.md`, the prefix
// did not match, the check was never asked, and the write LANDED. Measured before
// the fix: refused=false, landed=true.
//
// One character is the whole of it, and it is a character an agent produces
// without any intent to evade — `./` in front of a path is an ordinary way to
// write one. Any rule narrowed on a prefix was avoidable by spelling the path
// the other way, which is every narrowed rule in every project.
//
// The fix is to Clean the relative path before reporting it. Lexical, no
// filesystem access, and it cannot pull an outside path in: `../x` cleans to
// `../x` and stays outside, which T028_05 and T028_04 hold onto from the other
// side.
//
// This test drives the bypass directly: the refusal must reach the agent and
// nothing must land.
func TestT028_03b_ADotRelativeSpellingDoesNotEvadeANarrowedRule(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "guarded-dir", guardsSecret,
		map[string]string{"h.sh": refuseWithReason("this path is guarded")})

	res := e.Run(proj, "s-028-03b", "write the guarded path the other way", Turns("done",
		Write("w1", "./secret/keys.md", "hunter2\n"),
	))

	assert.True(t, res.Saw("this path is guarded"),
		"writing `./secret/keys.md` instead of `secret/keys.md` got past a rule narrowed on "+
			"`secret/` — a match is a prefix test, so an uncleaned spelling is a way round "+
			"every narrowed rule in the project")
	assert.False(t, e.Exists(proj, "secret/keys.md"),
		"the guarded write landed despite the rule")
}

// T028_04: a path outside the workspace keeps its absolute spelling.
//
// The other direction, and a security property rather than a tidiness one.
// `filepath.Rel` would happily return `../../../etc/passwd`, and a match is a
// prefix test — a rule written for a folder in the project must never be handed
// a spelling that could climb into one.
//
// Leaving it absolute is the honest answer: no project-relative match admits
// it, because the write is outside the rule's subject.
//
// The assertion is that the reported path is ABSOLUTE and names the outside
// file, not merely that it differs from some relative form. An engine reporting
// `../outside/note.md` would fail this, which is the point.
func TestT028_04_APathOutsideTheWorkspaceStaysAbsolute(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "observer", seeEveryCreate, map[string]string{"h.sh": recordPayload})

	outside := filepath.Join(t.TempDir(), "note.md")
	e.Run(proj, "s-028-04", "write outside the project", Turns("done",
		Write("w1", outside, "not the project's business\n"),
	))

	got := pathsSeen(t, e, proj, "observer")
	require.Len(t, got, 1, "the write must reach the rule")

	assert.True(t, filepath.IsAbs(got[0]),
		"a path outside the workspace must keep its absolute spelling, so that no "+
			"project-relative match can admit it: got %q", got[0])
	assert.False(t, strings.HasPrefix(got[0], ".."),
		"a path outside the workspace must never be reported as a climbing relative path — "+
			"a match is a prefix test and `..` is how one gets fooled: got %q", got[0])
}

// T028_05: a project-relative match does not admit a write outside the project.
//
// The consequence of T028_04, asserted through a match. The rule guards
// `secret/`; a file called `secret/keys.md` in a DIFFERENT directory entirely
// must not be caught by it.
//
// This is the test that fails if the engine ever starts relativising outside
// paths: the outside write would be reported as `secret/keys.md`, the prefix
// would match, and a rule about this project would be refusing writes in
// someone else's tree.
func TestT028_05_AProjectMatcherDoesNotReachOutsideTheProject(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "guarded-dir", guardsSecret,
		map[string]string{"h.sh": refuseWithReason("this path is guarded")})

	// The same trailing shape, in a tree that is not this project.
	elsewhere := filepath.Join(t.TempDir(), "secret", "keys.md")
	res := e.Run(proj, "s-028-05", "write into another tree", Turns("done",
		Write("w1", elsewhere, "not this project's secret\n"),
	))

	assert.False(t, res.Saw("this path is guarded"),
		"a rule about this project's secret/ refused a write in a different tree — the outside "+
			"path was given a project-relative spelling, which lets a prefix match reach "+
			"anywhere on the filesystem")
}

// T028_06: a symlink that escapes the repository is not reported as being
// inside it.
//
// The case the engine's own comment calls "the worse of the two", and the reason
// containment is resolved on the filesystem rather than checked lexically. A
// repository containing `escape -> /outside`, written to at
// `<root>/escape/id_rsa`: `filepath.Rel` returns `escape/id_rsa`, with no `..`
// anywhere in it, so every string check passes and the event carries a clean
// relative path naming a file OUTSIDE the repository. A check joins it against
// its own root and reads the outside file, and nothing reports a problem.
//
// The engine's comment records that this was measured rather than argued: with
// the lexical version it returned "escape/id_rsa" for a file in a different temp
// directory entirely. This is that measurement, driven end to end.
//
// The claim is narrow and is the one that matters: the reported path must not be
// a project-relative spelling, because a project-relative match would then
// admit a file that is not in the project.
//
// # What this test can and cannot discriminate
//
// It is asserted as "not relative" rather than against one particular wrong
// string, and that is deliberate — but it is worth being honest about its reach,
// because a mutation run measured it. Replacing the resolve-based containment
// check with the LEXICAL one the engine's comment warns about does NOT fail this
// test: with the symlink unresolvable through that path, the function falls
// through to its last branch and returns the absolute spelling anyway. The
// engine arrives at a safe answer by a different route.
//
// So this pins the OUTCOME — no project-relative spelling for a file outside the
// project — and not the mechanism that produces it. That is the right thing for
// an end-to-end test to hold, and the mechanism itself is a unit-level concern
// where `reportable` can be called directly with a root and a path. Recording
// the limit here rather than leaving a reader to assume the test is stronger
// than it is.
func TestT028_06_ASymlinkEscapingTheRepositoryIsNotReportedAsInside(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "observer", seeEveryCreate, map[string]string{"h.sh": recordPayload})

	// A real directory outside the project, reached from inside it by a symlink.
	outside := t.TempDir()
	link := filepath.Join(proj, "escape")
	require.NoError(t, os.Symlink(outside, link),
		"this test needs a symlink; without one it is not exercising the case")

	e.Run(proj, "s-028-06", "write through the escaping symlink", Turns("done",
		Write("w1", filepath.Join(link, "id_rsa"), "a key that is not in this repo\n"),
	))

	got := pathsSeen(t, e, proj, "observer")
	require.Len(t, got, 1, "the write must reach the rule")

	assert.True(t, filepath.IsAbs(got[0]),
		"a write through a symlink pointing OUT of the repository was reported as a "+
			"project-relative path — a match would admit it as project content, and a check "+
			"joining it against the project root would read a file that is not in the "+
			"project: got %q", got[0])
}

// T028_06b: a project-relative match does not admit a write that escaped
// through a symlink.
//
// T028_06's consequence, and the assertion that actually matters to a rule
// author. A guardrail written for `escape/` — an ordinary directory name in the
// project, as far as the declaration can tell — must not be handed a file that
// lives outside the repository.
//
// Distinct from T028_06 rather than a restatement: that one reads the spelling
// off the payload, this one goes through the match, and an engine could report
// one spelling to the check while narrowing on another.
func TestT028_06b_AProjectMatcherDoesNotAdmitAnEscapedWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "guards-escape", `on:
  - event: PreFileCreate
    match: event.path startsWith "escape/"
  - event: PreFileUpdate
    match: event.path startsWith "escape/"
checks:
  - script: ./h.sh
`, map[string]string{"h.sh": refuseWithReason("inside the project")})

	outside := t.TempDir()
	link := filepath.Join(proj, "escape")
	require.NoError(t, os.Symlink(outside, link),
		"this test needs a symlink; without one it is not exercising the case")

	res := e.Run(proj, "s-028-06b", "write through the escaping symlink", Turns("done",
		Write("w1", filepath.Join(link, "id_rsa"), "a key that is not in this repo\n"),
	))

	assert.False(t, res.Saw("inside the project"),
		"a rule narrowed on a project directory was applied to a file OUTSIDE the repository, "+
			"reached through a symlink of that name — the rule's subject is project content, "+
			"and this file is not that")
}

// T028_07: a symlink INSIDE the repository is still reported as inside.
//
// The control for T028_06, and it is what stops the fix being "distrust every
// symlink". A link that stays within the tree is ordinary — a docs directory
// linked to a subdirectory, a vendored path — and the writes through it are
// genuinely project content that narrowed rules must still admit.
//
// Without this, an engine reporting every symlinked path as absolute would
// satisfy T028_06 and would quietly stop enforcing rules on a large class of
// real repositories.
func TestT028_07_ASymlinkInsideTheRepositoryStaysInside(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "observer", seeEveryCreate, map[string]string{"h.sh": recordPayload})

	// A real directory in the project, and a link to it from another name in the
	// same project.
	require.NoError(t, os.MkdirAll(filepath.Join(proj, "real"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(proj, "real"), filepath.Join(proj, "linked")),
		"this test needs a symlink; without one it is not exercising the case")

	e.Run(proj, "s-028-07", "write through an internal symlink", Turns("done",
		Write("w1", filepath.Join(proj, "linked", "note.md"), "still project content\n"),
	))

	got := pathsSeen(t, e, proj, "observer")
	require.Len(t, got, 1, "the write must reach the rule")

	assert.False(t, filepath.IsAbs(got[0]),
		"a write through a symlink that stays INSIDE the repository must still be reported "+
			"relative — reporting it absolute would take a whole class of ordinary repositories "+
			"out of every narrowed rule's scope: got %q", got[0])
	assert.True(t, strings.HasSuffix(got[0], "note.md"),
		"the reported path must still name the file that was written: got %q", got[0])
}

// T028_08: a write aimed at a path that is a DIRECTORY produces no file event.
//
// A deliberate silence, not an oversight, and this test records it as intended
// behaviour rather than as an accident nobody had looked at. No file write can
// land on a directory — the harness's own write fails — so there is no file
// modification to report.
//
// The alternative the engine used to have was worse and is what the silence is
// for: emitting PreFileUpdate here invented an existing FILE out of a stat that
// only said "something is here", and put every PreFileUpdate rule to work on a
// directory. A rule asking about a file's markers or its content would then be
// judging something that has neither.
//
// A first version of this test asserted the opposite — that the rule must be
// asked, on the reasoning that the engine reports actions rather than predicting
// the filesystem. That reasoning is not wrong in general and IS wrong here,
// because the event's own vocabulary is about files: there is no honest file
// event to emit for a directory. The correction is recorded rather than quietly
// made, since the same argument will occur to the next reader.
//
// The write still fails, and it fails as the harness's own error about a real
// filesystem condition rather than as a guardrail verdict — which T028_09 pins.
func TestT028_08_AWriteAimedAtADirectoryProducesNoFileEvent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "observer", seeEveryCreate, map[string]string{"h.sh": recordPayload})

	require.NoError(t, os.MkdirAll(filepath.Join(proj, "adir"), 0o755))

	e.Run(proj, "s-028-08", "write to a path that is a directory", Turns("done",
		Write("w1", "adir", "this cannot land\n"),
	))

	assert.Empty(t, pathsSeen(t, e, proj, "observer"),
		"a write aimed at a directory must produce no FILE event — there is no file "+
			"modification to report, and inventing a PreFileUpdate would put every rule about "+
			"file content to work on a directory that has none")
}

// T028_09: the directory write is not turned into a guardrail refusal.
//
// The other half of T028_08. "No event" must mean the engine stays out of it,
// not that the engine quietly refuses: a write that cannot land should fail as
// the harness's own error about a real filesystem condition, and an agent
// reading a guardrail refusal would go looking for a rule that does not exist.
//
// The project has a rule bound to creations, so an engine that emitted an event
// here would reach a check; the check refuses, which is what makes a refusal
// visible if one is ever produced.
func TestT028_09_ADirectoryWriteIsNotAGuardrailRefusal(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "refuses-creates", `on:
  - event: PreFileCreate
  - event: PreFileUpdate
checks:
  - script: ./h.sh
`, map[string]string{"h.sh": refuseWithReason("refused by the rule")})

	require.NoError(t, os.MkdirAll(filepath.Join(proj, "adir"), 0o755))

	res := e.Run(proj, "s-028-09", "write to a path that is a directory", Turns("done",
		Write("w1", "adir", "this cannot land\n"),
	))

	assert.False(t, res.Saw("refused by the rule"),
		"a write aimed at a directory reached a guardrail — it must fail as the filesystem "+
			"condition it is, not as a rule's verdict, or the agent goes looking for a rule "+
			"that never applied")
}
