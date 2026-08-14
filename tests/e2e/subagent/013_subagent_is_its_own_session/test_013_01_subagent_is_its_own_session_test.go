package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// subagent_is_its_own_session: work delegated to a sub-agent is guarded as a
// session in its own right — its own record, its own identity, its own state.
//
// # What this package proves, and what it does not
//
// Stated plainly, because an earlier version of this file claimed more than it
// established and the difference is the whole value of an e2e.
//
// These tests are driven through the mock, so everything they assert is about
// the wiring a user installs: that this repo's plugin subscribes to the moment a
// sub-agent's cycle ends, that the command it names exists, and that a real
// delegated cycle — including one bound into its own `git worktree` — completes
// with that binding live rather than trapping the sub-agent in a retry loop.
// That last point is not decoration: SubagentStop treats a non-zero exit as a
// block and re-runs the sub-agent's turn, so a hook that failed here would make
// delegation impossible rather than merely unguarded.
//
// What they do NOT prove is the routing inside the engine — that a sub-agent's
// hook reads the SUB-AGENT's record rather than the dispatching session's. Two
// facts, both measured against this harness, put that out of reach here:
//
//  1. The mock reports the sub-agent's path in BOTH `transcript_path` and
//     `agent_transcript_path`, byte-identical. So record()'s preference between
//     the two fields is a no-op under the mock: mutate it to return
//     p.TranscriptPath and this whole package stays green. A test asserting the
//     preference through this harness would be asserting nothing.
//  2. Inside a sub-agent the mock fires SubagentStop and nothing else — no
//     PreToolUse, so no guardrail of the sub-agent's own runs — and a
//     SubagentStop hook that exits zero has neither stream forwarded anywhere
//     the run can see. The only channel out of that hook that reaches the stream
//     is a non-zero exit, which is a BLOCK and re-runs the turn.
//  3. The mock does not APPLY a sub-agent's tool calls. It runs the sub-agent's
//     script for the result it reports; a Write in a sub-agent's scenario
//     creates no file. So the sub-agent's effects on a tree are not observable
//     either, and assertions here are written against what the dispatch itself
//     bound — the worktree — rather than against files.
//
// Together those mean a sub-agent's identity cannot be observed from inside a
// test through the plugin's own wiring. That is a finding about the harness, not
// a reason to hand-wire a hook around it: a test that attached its own lifecycle
// hook would be arranging wiring no user has, and whatever it then proved would
// be about the arrangement rather than about the product.
//
// The routing itself is covered where it can be covered honestly and where a
// mutation to it actually fails something:
//
//   - services/sr-session/subagent_test.go — record() prefers the sub-agent's own
//     path, reconstructs one from an agent id, refuses a traversing id, and
//     resolves a distinct identity end to end on real files in the real nested
//     layout.
//   - internal/transcript/subagent_test.go — a sub-agent's identity is its own
//     origin, survives a fork, and two sub-agents of one parent are distinct.
//   - services/sr-session/session_subagent_stop_test.go — an unplaceable cycle
//     stands down rather than blocking, and never acts as the parent.
//
// The gap that would close this properly is in the plugin, and belongs there:
// nothing sloprail registers today can report what it decided from inside a
// sub-agent without blocking it. When `subagent-stop` grows a real cycle — it is
// a stub pending the tree-diffing on other branches — the effects it writes
// become observable state, and the identity claim can be made here against a
// user's own wiring. Until then this file states the smaller thing it can
// actually show.

// T013_01: this repo's plugin binds the moment a sub-agent's cycle ends.
//
// The concrete hole this closes. Stop fires only in the root agent, so with no
// SubagentStop binding a sub-agent's cycle ended with no guardrail running at
// all — work delegated to a sub-agent was simply not guarded, and if it held its
// own worktree the root's later Stop would never see it either.
//
// Read out of the plugin's own wiring. The claim is that THIS repo's plugin asks
// to hear the event, and a test attaching its own hook would prove the harness
// fires it while proving nothing about whether sloprail ever subscribed — which
// is precisely what was missing.
//
// Paired with a live dispatch, because a binding naming a command that does not
// exist would satisfy the file check alone.
func TestT013_01_PluginBindsSubagentStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	initRepo(t, proj)

	commands := boundCommands(t, "SubagentStop")
	if len(commands) == 0 {
		t.Fatalf("the plugin binds nothing to SubagentStop — a sub-agent's cycle ends with no guardrail running")
	}
	if !slices.Contains(commands, "sr-session subagent-stop") {
		t.Fatalf("SubagentStop is bound to %v, not to the command that ends a sub-agent's cycle", commands)
	}

	// The command it names must exist, or the binding is a promise to run
	// something that is not there.
	if res := e.CLI(proj, "session", "subagent-stop", "--help"); res.Code != 0 {
		t.Fatalf("the bound command does not exist: %s", res.Output)
	}

	// And a real delegated cycle still completes with the plugin installed.
	subScript := filepath.Join(proj, "sub.sh")
	writeScenario(t, subScript, harness.Turns("sub done",
		Write("sw1", "from-sub.md", "delegated work"),
	))
	res := e.Run(proj, "s-013-01", "delegate", Turns("root done",
		Dispatch("d1", "do the job", subScript, "worktree"),
	))
	if !res.Saw("root done") {
		t.Fatalf("the delegated cycle did not complete with SubagentStop bound:\n%s", res.Output)
	}

	// This does NOT prove a blocking hook would be noticed, and it used to say
	// it did. Measured: mutate subagent-stop to return an error on every cycle
	// and this whole package stays green. A SubagentStop hook's exit status has
	// no observable consequence through this harness — exit 0, 1 and 2 are alike
	// invisible, no retry, no marker, and the root completes regardless.
	//
	// So what is actually proven is narrower: the plugin binds the hook, the
	// bound command exists, and a delegated cycle runs to completion with it in
	// place. Whether a refusal from it reaches anything is unobservable here.
	// Closing that needs a channel out of SubagentStop that the mock reports —
	// see the finding recorded with 014_subagent_dispatch_shapes.
}

// T013_02: a sub-agent dispatched into its own worktree completes cleanly with
// the plugin live.
//
// The isolated case is the one the whole design turns on, so it gets its own
// end-to-end run rather than riding on T013_01's: a sub-agent judging different
// content at the same repository-relative paths is the case where confusing it
// with its parent does real damage.
//
// What is checked here is that the isolation is REAL — the mock binds a genuine
// `git worktree add`, and the sub-agent's tree is not the project's — and that
// the cycle still ends. The identity claim that motivates the isolation is a
// unit-test matter, for the reasons in this file's opening note.
func TestT013_02_AnIsolatedSubagentCompletesWithTheGuardrailLive(t *testing.T) {
	e := New(t)
	proj := e.Project()
	initRepo(t, proj)

	subScript := filepath.Join(proj, "sub.sh")
	writeScenario(t, subScript, harness.Turns("sub done",
		Write("sw1", "from-sub.md", "the sub-agent's own work"),
	))

	res := e.Run(proj, "s-013-02", "delegate some work", Turns("root done",
		Dispatch("d1", "do the delegated thing", subScript, "worktree"),
		Write("rw1", "from-root.md", "the root's own work"),
	))

	if !res.Saw("root done") {
		t.Fatalf("the isolated delegated cycle did not complete:\n%s", res.Output)
	}

	// The isolation is real, not merely requested: the mock bound an actual
	// `git worktree add`, and the directory it created is on disk. Without this
	// the "own worktree" case would never have been exercised, and the test
	// would pass on a dispatch that quietly shared the tree.
	//
	// Checked by the worktree's existence rather than by where the sub-agent's
	// file landed, because the mock does not APPLY a sub-agent's tool calls — it
	// runs the sub-agent's script for the result it reports and writes nothing.
	// An assertion on `from-sub.md` would hold for a sub-agent that was never
	// isolated, or never dispatched at all, and would be measuring the mock's
	// tool execution rather than the isolation.
	trees := worktrees(t, proj)
	if len(trees) != 1 {
		t.Fatalf("want one worktree bound for the isolated sub-agent, found %d (%v) — isolation=%q did not bind a separate tree", len(trees), trees, "worktree")
	}

	// The dispatching session's own tree is untouched by that, which is what
	// makes the worktree a SEPARATE tree rather than a renamed one.
	if _, err := os.Stat(filepath.Join(proj, ".git")); err != nil {
		t.Fatalf("the dispatching session's own repository is gone: %v", err)
	}
}

// worktrees lists the worktree directories the mock bound for this project's
// sub-agents. Absent directory means none were bound.
func worktrees(t *testing.T, proj string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(proj, ".claude", "worktrees"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read worktrees: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// T013_03: a session dispatching two sub-agents completes, and the two get
// separate trees.
//
// A session with two sub-agents has three sessions in play. The mock reports a
// distinct agent id per dispatch and binds a worktree per sub-agent, so what is
// observable here is that the two are kept apart by the harness at all — the
// precondition for the engine's per-session keying meaning anything. That the
// two resolve to distinct IDENTITIES is TestTwoSubagentsOfOneParentAreDistinct.
func TestT013_03_TwoSubagentsGetTwoTrees(t *testing.T) {
	e := New(t)
	proj := e.Project()
	initRepo(t, proj)

	first := filepath.Join(proj, "sub1.sh")
	second := filepath.Join(proj, "sub2.sh")
	writeScenario(t, first, harness.Turns("one done", Write("s1", "one.md", "first")))
	writeScenario(t, second, harness.Turns("two done", Write("s2", "two.md", "second")))

	res := e.Run(proj, "s-013-03", "delegate twice", Turns("root done",
		Dispatch("d1", "first job", first, "worktree"),
		Dispatch("d2", "second job", second, "worktree"),
	))

	if !res.Saw("root done") {
		t.Fatalf("a session dispatching two sub-agents did not complete:\n%s", res.Output)
	}

	// Two dispatches, two distinct agent ids. The mock announces each in the
	// tool result, and equal ids would mean the harness treated the two as one
	// sub-agent — under which nothing about keeping two sessions apart could be
	// exercised here at all.
	ids := agentIDs(res.Output)
	if len(ids) != 2 {
		t.Fatalf("want two dispatched sub-agents, saw %d (%v):\n%s", len(ids), ids, res.Output)
	}
	if ids[0] == ids[1] {
		t.Fatalf("both dispatches reported the agent id %s — the harness ran one sub-agent, not two", ids[0])
	}

	// Two worktrees, so the two sub-agents got trees of their own rather than
	// sharing one. A single worktree here would mean the second dispatch reused
	// the first's tree, under which two sub-agents' work would be one tree's
	// diff.
	trees := worktrees(t, proj)
	if len(trees) != 2 {
		t.Fatalf("want a worktree per sub-agent, found %d (%v) — two separate trees were never exercised", len(trees), trees)
	}
}

// T013_04: a sub-agent sharing the dispatching session's tree also completes.
//
// Sharing the tree is meant to be the ordinary path rather than a second
// mechanism: it differs from the isolated case in one dispatch field and nothing
// else. If a sub-agent needed machinery the root does not, the scheme would be
// the wrong one — so the shared case is run to the same end as the isolated one.
func TestT013_04_ASharedTreeSubagentCompletesToo(t *testing.T) {
	e := New(t)
	proj := e.Project()
	initRepo(t, proj)

	subScript := filepath.Join(proj, "sub.sh")
	writeScenario(t, subScript, harness.Turns("sub done",
		Write("sw1", "from-sub.md", "same tree, different session"),
	))

	// The only difference from T013_02: no isolation, so the sub-agent works in
	// the dispatching session's own tree.
	res := e.Run(proj, "s-013-04", "delegate in the same tree", Turns("root done",
		Dispatch("d1", "do it here", subScript, ""),
	))

	if !res.Saw("root done") {
		t.Fatalf("the shared-tree delegated cycle did not complete:\n%s", res.Output)
	}

	// The tree really is shared — no worktree was bound at all. Otherwise this
	// is T013_02 again under another name, and the shared-tree claim rests on
	// nothing.
	if trees := worktrees(t, proj); len(trees) != 0 {
		t.Fatalf("a worktree was bound (%v) for a dispatch that asked for none — the shared-tree case was never exercised", trees)
	}
}

// T013_05: the plugin still binds the root's own Stop.
//
// The companion to T013_01, and the reason SubagentStop had to be added rather
// than Stop widened: the two events fire in different agents, each in exactly
// one. A change that pointed SubagentStop at the right command while dropping
// Stop would leave root sessions unguarded, and every other test here would
// still pass.
func TestT013_05_PluginStillBindsTheRootStop(t *testing.T) {
	commands := boundCommands(t, "Stop")
	if !slices.Contains(commands, "sr-session stop") {
		t.Fatalf("Stop is bound to %v, not to the command that ends a root session's cycle", commands)
	}
}

// boundCommands reads what this repo's plugin binds to a lifecycle event, out of
// the plugin's own wiring file.
//
// Read from the tree under test rather than from an installed copy, so what is
// asserted is what this branch ships.
func boundCommands(t *testing.T, event string) []string {
	t.Helper()
	wiring, err := os.ReadFile(filepath.Join(
		repoRootOf(t), "marketplace", "plugins", "sloprail", "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("read the plugin's wiring: %v", err)
	}
	var hooks struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(wiring, &hooks); err != nil {
		t.Fatalf("the plugin's wiring is not valid json: %v", err)
	}
	var commands []string
	for _, b := range hooks.Hooks[event] {
		for _, h := range b.Hooks {
			commands = append(commands, h.Command)
		}
	}
	return commands
}

// agentIDs returns the agent ids the mock announced, in order.
//
// The mock reports "agentId: <id>" in the tool result of each dispatch, which is
// the harness's own statement about how many sub-agents it ran and which they
// were. Read from the stream rather than counted from the scenario, so a
// dispatch that silently did not happen shows up as a missing id.
// The label travels inside a JSON string, so the newline after the id is the
// two characters \ and n rather than a real one; both it and a literal newline
// end the id.
func agentIDs(output string) []string {
	const label = "agentId: "
	var ids []string
	for rest := output; ; {
		i := strings.Index(rest, label)
		if i < 0 {
			return ids
		}
		rest = rest[i+len(label):]
		id := rest
		if end := strings.IndexAny(id, " \"\n"); end >= 0 {
			id = id[:end]
		}
		if end := strings.Index(id, `\n`); end >= 0 {
			id = id[:end]
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
}

// writeScenario renders a scenario to a script the mock runs as a sub-agent.
func writeScenario(t *testing.T, path string, s harness.Scenario) {
	t.Helper()
	if err := s.Script(path); err != nil {
		t.Fatalf("write scenario %s: %v", path, err)
	}
}

// initRepo makes the project a real git repository with a commit, which is what
// isolation="worktree" needs to bind a genuine worktree. Without it the mock
// falls back to a plain empty directory and the isolation being tested is a
// different, weaker thing.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "e2e@example.invalid"},
		{"config", "user.name", "e2e"},
		{"add", "-A"},
		{"commit", "-m", "initial", "--no-gpg-sign"},
	} {
		run(t, dir, "git", args...)
	}
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// repoRootOf locates this repository, so the plugin's wiring is read from the
// tree under test rather than from wherever a test happens to run.
func repoRootOf(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}
