package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T037_04: when a gate's write is BOTH underivable (its settled
// bytes cannot be computed ahead of time — here, a Bash pipeline through `tr`,
// which internal/commandmod cannot resolve to a literal payload) AND its
// `require` precondition is unmet, the refusal must name the MISSING
// PREREQUISITE, not the generic "could not verify this write" reason.
//
// This is the P2 finding from the strategy repo's clean-install smoke test
// (memories/tasks/distribution/soft-launch-pain-priorities/02_smoke-test.md,
// Part 2c): a raw Bash write, no skill loaded, was refused with
//
//	"...could not verify this write before it lands..."
//
// which is fail-closed and correct, but does not tell a user that loading the
// skill is the fix. `require` needs no content at all (it reads the trajectory,
// not the write), so it is evaluated BEFORE any check — on a gate the unmet
// prerequisite is the refusal, whether or not the write's bytes are known. This
// test pins that the actionable reason reaches the agent.
//
// The write here is an UPDATE, not a create, deliberately: internal/filemod's
// extractCommand reports NO Pre event at all for a command-derived CREATE whose
// payload cannot be resolved (see its own comment: "a command line does not say
// what bytes will result", so a silent creation stays silent rather than
// carrying an unknown content) — which is the case engine_repo_judges'
// underivableWrite exercises, and which the Stop after-check alone covers, never
// isUnderivablePreWrite. An UPDATE to a file that already exists is different:
// PreFileUpdate DOES fire, carrying resultKnown=false when the payload cannot be
// resolved — that is the actual isUnderivablePreWrite branch this test needs to
// reach. So the file is created (and committed, out of the cycle diff) BEFORE
// the underivable Bash write updates it.
// sr:proves checks/requirements-before-checks
func TestT037_04_UnderivableWriteWithUnmetRequireNamesTheSkill(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "require-topic", pureRequireGate, nil)
	// Committed with the guards, so an underivable UPDATE to the file is a change
	// against a committed baseline rather than this cycle's own uncommitted create
	// — the file must already exist on disk for PreFileUpdate, rather than
	// PreFileCreate, to be the kind extractCommand reports.
	e.WriteFile(proj, "memories/topics/idea.md", "# an idea\n")
	e.CommitAll(proj, "the guards")

	res := e.Run(proj, "s-037-04", "update a topic the hard way, no skill loaded", Turns("done",
		underivableUpdate037("memories/topics/idea.md"),
	))

	if !res.Refused() {
		t.Fatalf("a gate requiring an unloaded skill did not block an underivable update:\n%s", res.Output)
	}
	if !res.Saw("document-topic") {
		t.Errorf("the refusal does not name the required skill — it gave the generic "+
			"unverifiable-write reason instead of the actionable one:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "could not verify this write before it lands") {
		t.Errorf("the refusal used the generic content-unverifiable reason even though "+
			"the require precondition — which needs no content — already explains the "+
			"refusal and names the actual fix:\n%s", res.Output)
	}
}

// T037_05: the control — the SAME underivable update, with the skill loaded first,
// is PERMITTED by a pure-require gate: its decision (was the skill loaded?) needs
// no content, so an unknown result gives it nothing to fail on. This is what proves
// T037_04 refused for the require specifically. A gate whose decision reads the
// content DOES refuse the same write (fileguard/034 T034_18), and the plain
// file-guard beside the rule judges what settled at Stop.
func TestT037_05_UnderivableWriteWithMetRequireIsPermittedByAPureRequireGate(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "require-topic", pureRequireGate, nil)
	// Committed with the guards, so an underivable UPDATE to the file is a change
	// against a committed baseline rather than this cycle's own uncommitted create
	// — the file must already exist on disk for PreFileUpdate, rather than
	// PreFileCreate, to be the kind extractCommand reports.
	e.WriteFile(proj, "memories/topics/idea.md", "# an idea\n")
	e.CommitAll(proj, "the guards")

	res := e.Run(proj, "s-037-05", "load the skill, then update a topic the hard way", Turns("done",
		Skill("s1", "document-topic"),
		underivableUpdate037("memories/topics/idea.md"),
	))

	if res.Refused() {
		t.Fatalf("a pure-require gate whose skill require was met refused an underivable update — "+
			"its decision needs no content:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "SKILL REQUIRED") {
		t.Errorf("the refusal carries the skill-remedy text even though require was satisfied:\n%s", res.Output)
	}
}

// underivableUpdate037 is a shell command whose output bytes the engine will not
// predict — a pipeline through `tr`, which internal/commandmod cannot resolve to
// a literal payload (mirrors engine_repo_judges' own underivableWrite helper).
// Run against a file that already exists, this reaches PreFileUpdate with
// resultKnown=false — isUnderivablePreWrite's actual branch. Reimplemented
// locally rather than imported: it lives in a sibling package under this
// plugin's own e2e module boundary, the same reason 037's main_test.go already
// keeps its own local commitGuards instead of reaching across packages.
func underivableUpdate037(path string) harness.Turn {
	return Bash("w1", "printf '%s\\n' 'updated' | tr -d '\\r' > "+path)
}
