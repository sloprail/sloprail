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
// session in its own right — its own identity, its own state — and neither its
// verdicts nor the parent's stand in for the other's.
//
// Driven through the mock rather than by calling the binary, because the claim
// is about what a harness actually hands a sub-agent's hook. A test that
// constructed the payload itself would prove the routing correct for a payload
// nothing sends. The mock dispatches a real sub-agent: it seeds the sidechain
// transcript where Claude Code puts it, binds a real `git worktree add` for
// isolation="worktree", and fires SubagentStop with agent_id and
// agent_transcript_path — all of which was verified against the payload it
// emits before these assertions were written.
//
// The identity is read back with `sloprail session id`, which is the command
// everything per-session is keyed by. Observing the DATABASE directly is not
// possible from this branch: the engine sets no environment on a hook process
// here, so `session state` cannot resolve its scope from inside one. That gap
// and its fix live on impl/hook-env. What `session id` shows is the input that
// path is keyed on, which is where a sub-agent could be confused with its
// parent.

// idScript reports the identity of whatever session the hook belongs to,
// alongside the working directory it ran in, appending both to a file the test
// reads. Two facts per line, because the pair is what the claim is about: a
// distinct identity, and the tree it goes with.
//
// It first gives the sub-agent's transcript the origin record the mock does not
// write, and the gap is worth stating exactly rather than papering over.
//
// The mock writes no `uuid` and no `parentUuid` on ANY record, sub-agent or
// otherwise: its seeded sidechain record carries agentId, sessionId,
// isSidechain, cwd and the dispatch prompt, and nothing that can take part in a
// message chain. Real Claude Code writes both — across the 305 sub-agent
// transcripts measured on one machine, every origin record carried a uuid and an
// explicitly null parentUuid, never an absent one. The root harness works around
// the same gap by seeding a transcript itself (see seedTranscript); this is that
// same workaround for the sub-agent case.
//
// What is NOT faked matters more than what is. The record goes in the mock's own
// chosen location, for an agent the mock itself dispatched, with a uuid derived
// from the agent id the MOCK generated — so the dispatch decides which sessions
// exist and how many, and the test supplies only the one field the mock omits,
// in the shape real transcripts were measured to have. That a sub-agent is
// dispatched at all, that it gets a real git worktree, and that SubagentStop
// fires carrying agent_id and agent_transcript_path are all the mock's,
// unmodified — and those are what the routing under test consumes.
//
// Seeding from inside the hook is what makes it possible at all: the agent id
// does not exist until the mock has dispatched.
func idScript(sink string) string {
	return `#!/bin/sh
payload=$(cat)
tp=$(printf '%s' "$payload" | sed -n 's/.*"agent_transcript_path":"\([^"]*\)".*/\1/p')
aid=$(printf '%s' "$payload" | sed -n 's/.*"agent_id":"\([^"]*\)".*/\1/p')
if [ -n "$tp" ] && [ -n "$aid" ]; then
  printf '{"type":"user","uuid":"origin-%s","parentUuid":null,"isSidechain":true}\n' "$aid" >> "$tp"
fi
id=$(printf '%s' "$payload" | sloprail session id 2>&1)
printf 'id=%s cwd=%s\n' "$id" "$PWD" >> ` + sink + `
exit 0
`
}

// T013_01: a sub-agent in its own worktree resolves to its own identity, not
// the identity of the session that dispatched it.
//
// This is the case the whole design turns on. The sub-agent is judging different
// content at the same repository-relative paths, so were it to resolve to the
// parent's identity its baseline, read mark and file verdicts would be written
// into the parent's state — and a pass it recorded would exempt the parent from
// a check on bytes nobody ever looked at.
func TestT013_01_SubagentInItsOwnWorktreeIsItsOwnSession(t *testing.T) {
	e := New(t)
	proj := e.Project()
	initRepo(t, proj)

	sink := filepath.Join(proj, "ids.txt")
	subScript := filepath.Join(proj, "sub.sh")
	writeScenario(t, subScript, harness.Turns("sub done",
		Write("sw1", "from-sub.md", "the sub-agent's own work"),
	))

	// Both ends of the delegation report who they are. Stop fires only in the
	// dispatching session, SubagentStop only in the sub-agent, so the two lines
	// are the two sessions.
	e.ExtraHook(proj, "SubagentStop", "*", "sh "+writeHook(t, proj, "subagent-id.sh", idScript(sink)))

	e.Run(proj, "s-013-01", "delegate some work", Turns("root done",
		Dispatch("d1", "do the delegated thing", subScript, "worktree"),
		Write("rw1", "from-root.md", "the root's own work"),
	))

	subID, subCwd := readOne(t, sink)

	// The parent's identity, asked the same way, from the same conversation.
	rootID := rootIdentity(t, e, proj, "s-013-01", sink)

	if subID == "" || rootID == "" {
		t.Fatalf("an identity did not resolve: sub=%q root=%q", subID, rootID)
	}
	if subID == rootID {
		t.Fatalf("the sub-agent resolved to its parent's identity (%s) — everything it keys per session would land in the parent's state", subID)
	}

	// And the isolation is real, not merely reported: the sub-agent's hook ran
	// in a different directory from the project. If it had not, this test would
	// be making the worktree claim about a case it never exercised.
	if subCwd == "" || sameDir(subCwd, proj) {
		t.Fatalf("the sub-agent did not run in its own worktree (cwd=%q, project=%q) — the isolated case was never exercised", subCwd, proj)
	}
}

// T013_02: a sub-agent SHARING the dispatching session's tree is still a session
// of its own.
//
// The case worth arguing, because sharing the tree is a real argument for
// sharing the verdicts — the content genuinely is the same bytes. They stay
// apart because a verdict is about content as judged BY A RULE IN A
// CONVERSATION: a judge check is a model call rather than a pure function, and
// the memory a rule keeps ("I already warned about X") is a fact about a
// conversation, not about the tree. A sub-agent inheriting that would skip a
// warning it was never given.
//
// Sharing the tree must not need a mechanism of its own either. It differs from
// the isolated case in one dispatch field and nothing else — if a sub-agent
// needed machinery the root does not, the scheme would be the wrong one.
func TestT013_02_SubagentSharingTheTreeIsStillItsOwnSession(t *testing.T) {
	e := New(t)
	proj := e.Project()
	initRepo(t, proj)

	sink := filepath.Join(proj, "ids.txt")
	subScript := filepath.Join(proj, "sub.sh")
	writeScenario(t, subScript, harness.Turns("sub done",
		Write("sw1", "from-sub.md", "same tree, different session"),
	))

	e.ExtraHook(proj, "SubagentStop", "*", "sh "+writeHook(t, proj, "subagent-id.sh", idScript(sink)))

	// The only difference from T013_01: no isolation, so the sub-agent works in
	// the dispatching session's own tree.
	e.Run(proj, "s-013-02", "delegate in the same tree", Turns("root done",
		Dispatch("d1", "do it here", subScript, ""),
	))

	subID, subCwd := readOne(t, sink)
	rootID := rootIdentity(t, e, proj, "s-013-02", sink)

	if subID == "" || rootID == "" {
		t.Fatalf("an identity did not resolve: sub=%q root=%q", subID, rootID)
	}
	// The tree really is shared — otherwise this is T013_01 again under another
	// name, and the shared-tree claim rests on nothing.
	if !sameDir(subCwd, proj) {
		t.Fatalf("the sub-agent ran in %q, not the project tree %q — the shared-tree case was never exercised", subCwd, proj)
	}
	if subID == rootID {
		t.Fatalf("a sub-agent sharing the tree resolved to its parent's identity (%s) — its guardrail memory and verdicts would pool with the parent's", subID)
	}
}

// T013_03: two sub-agents of one session are two sessions, not one.
//
// A session dispatching two sub-agents has three sessions in play. Were the two
// sub-agents to collide, one's verdicts would exempt the other's files — and in
// separate worktrees those are different bytes at the same paths.
func TestT013_03_TwoSubagentsAreTwoSessions(t *testing.T) {
	e := New(t)
	proj := e.Project()
	initRepo(t, proj)

	sink := filepath.Join(proj, "ids.txt")
	first := filepath.Join(proj, "sub1.sh")
	second := filepath.Join(proj, "sub2.sh")
	writeScenario(t, first, harness.Turns("one done", Write("s1", "one.md", "first")))
	writeScenario(t, second, harness.Turns("two done", Write("s2", "two.md", "second")))

	e.ExtraHook(proj, "SubagentStop", "*", "sh "+writeHook(t, proj, "subagent-id.sh", idScript(sink)))

	e.Run(proj, "s-013-03", "delegate twice", Turns("root done",
		Dispatch("d1", "first job", first, "worktree"),
		Dispatch("d2", "second job", second, "worktree"),
	))

	lines := readLines(t, sink)
	if len(lines) != 2 {
		t.Fatalf("want one SubagentStop per sub-agent, got %d: %v", len(lines), lines)
	}
	oneID, oneCwd := parseLine(lines[0])
	twoID, twoCwd := parseLine(lines[1])

	if oneID == "" || twoID == "" {
		t.Fatalf("an identity did not resolve: %v", lines)
	}
	if oneCwd == twoCwd {
		t.Fatalf("both sub-agents ran in %q — two separate worktrees were never exercised", oneCwd)
	}
	if oneID == twoID {
		t.Fatalf("two sub-agents of one session share the identity %s — one's verdicts would exempt the other's files", oneID)
	}
}

// rootIdentity resolves the dispatching session's own identity, by running the
// same hook at the session's own Stop in a second turn of the same conversation.
//
// Read from the same conversation rather than computed, so the comparison is
// between two identities the engine actually produced.
func rootIdentity(t *testing.T, e *harness.Env, proj, sessionID, sink string) string {
	t.Helper()
	rootSink := sink + ".root"
	e.ExtraHook(proj, "Stop", "*", "sh "+writeHook(t, proj, "root-id.sh", idScript(rootSink)))
	e.Run(proj, sessionID, "carry on", harness.Turns("done",
		Write("rw2", "again.md", "more root work"),
	))
	id, _ := readOne(t, rootSink)
	return id
}

// writeHook puts a hook script in the project and returns its path.
func writeHook(t *testing.T, proj, name, body string) string {
	t.Helper()
	path := filepath.Join(proj, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write hook %s: %v", name, err)
	}
	return path
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

// readLines returns the non-empty lines the hooks appended, or fails when
// nothing was written — an absent file means the hook never ran, which would
// make every assertion below it vacuous.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no hook output at %s: %v — the hook never ran, so nothing here was tested", path, err)
	}
	var lines []string
	for _, l := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		t.Fatalf("hook output at %s is empty — nothing was tested", path)
	}
	return lines
}

func readOne(t *testing.T, path string) (id, cwd string) {
	t.Helper()
	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("want one hook line at %s, got %d: %v", path, len(lines), lines)
	}
	return parseLine(lines[0])
}

// parseLine reads back the "id=… cwd=…" the hook wrote.
func parseLine(line string) (id, cwd string) {
	for _, field := range strings.Fields(line) {
		switch {
		case strings.HasPrefix(field, "id="):
			id = strings.TrimPrefix(field, "id=")
		case strings.HasPrefix(field, "cwd="):
			cwd = strings.TrimPrefix(field, "cwd=")
		}
	}
	// A command that failed writes its error where the id would be; treat that
	// as no identity rather than as one, so a failure cannot pass for a value.
	if strings.Contains(id, "sloprail:") || strings.Contains(id, "transcript:") {
		return "", cwd
	}
	return id, cwd
}

// sameDir compares two directories after resolving symlinks, because macOS
// reports /var where the filesystem holds /private/var and the two would
// otherwise look like different trees.
func sameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = a
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = b
	}
	return filepath.Clean(ra) == filepath.Clean(rb)
}

// T013_04: the plugin binds the moment a sub-agent's cycle ends.
//
// The concrete hole this closes. Stop fires only in the root agent, so with no
// SubagentStop binding a sub-agent's cycle ended with no guardrail running at
// all — work delegated to a sub-agent was simply not guarded, and if it held its
// own worktree the root's later Stop would never see it either.
//
// Read out of the plugin's own wiring rather than through an ExtraHook. The
// claim is that THIS repo's plugin asks to hear the event, and a test attaching
// its own hook would prove the harness fires it while proving nothing about
// whether sloprail ever subscribed — which is precisely what was missing.
//
// Paired with a live dispatch, because the binding naming a command that does
// not exist would satisfy the file check alone. The sub-agent must still finish:
// SubagentStop treats a non-zero exit as a block and re-runs the turn, so a
// command that failed here would trap every delegated task in a retry loop
// instead of merely leaving it unjudged.
func TestT013_04_PluginBindsSubagentStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	initRepo(t, proj)

	wiring, err := os.ReadFile(filepath.Join(repoRootOf(t), "marketplace", "plugins", "sloprail", "hooks", "hooks.json"))
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
	bound, ok := hooks.Hooks["SubagentStop"]
	if !ok || len(bound) == 0 {
		t.Fatalf("the plugin binds nothing to SubagentStop — a sub-agent's cycle ends with no guardrail running:\n%s", wiring)
	}
	var commands []string
	for _, b := range bound {
		for _, h := range b.Hooks {
			commands = append(commands, h.Command)
		}
	}
	if !slices.Contains(commands, "sloprail session subagent-stop") {
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
	res := e.Run(proj, "s-013-04", "delegate", Turns("root done",
		Dispatch("d1", "do the job", subScript, "worktree"),
	))
	if !res.Saw("root done") {
		t.Fatalf("the delegated cycle did not complete with SubagentStop bound — a hook that blocks here traps every sub-agent in a retry loop:\n%s", res.Output)
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
