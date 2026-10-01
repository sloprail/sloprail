package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// ownStartCheck refuses a changeset holding FORBIDDEN text and names every file it
// refused, so a test can tell WHICH commits a sub-agent was judged on.
const ownStartCheck = `#!/bin/sh
payload=$(cat)
bad=$(printf '%s' "$payload" | jq -r '[.changeset.files[] | select(.newContent | contains("FORBIDDEN")) | .path] | join(", ")')
if [ -n "$bad" ]; then
  echo "{\"reason\":\"FORBIDDEN in: $bad\"}"
  exit 1
fi
exit 0
`

// T015_12: an isolated sub-agent begins its OWN session in its own worktree, so its
// file-guard range starts at the HEAD that worktree had at its first tool call —
// not at the commit its PARENT session began on.
//
// The parent began at A, hours (here: commits) before the sub-agent was spawned.
// Main gained B and C since, each violating the rule, and the sub-agent's worktree
// was created at C. Judged from the parent's start, the sub-agent was refused for
// B and C, files it never touched and cannot fix within its own change. Its own
// work is D.
//
// The rule is a plugin's, which has no folder in this repository, so the session
// start is the only floor of its range: a rule committed in the project is floored at
// its own commit instead, whatever the start.
func TestT015_12_AnIsolatedSubagentRangeStartsAtItsOwnWorktreeHead(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.EnablePluginShippingFileGuard(proj, "ownstart", "forbidden",
		"match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n",
		map[string]string{"check.sh": ownStartCheck})
	// Committed, so the sub-agent's worktree (created from HEAD) carries the settings
	// that enable the plugin: this is commit A, where the parent session begins.
	a := e.CommitAll(proj, "A: the project")

	// D now; the fix only once the Stop has refused it (see refusedThenFixes).
	sub := refusedThenFixes(t,
		Turns("sub done", harness.CommitFile("sd", "docs/d.md", "FORBIDDEN in D", "D: the sub-agent's own change")),
		Turns("sub fixed", harness.CommitFile("sf", "docs/d.md", "clean now", "D2: fix D")))
	res := e.Run(proj, "s-015-12", "delegate", Turns("root done",
		Bash("b0", "true"), // the parent's first tool call: its session start is A
		harness.CommitFile("cb", "docs/b.md", "FORBIDDEN in B", "B: lands on main after A"),
		harness.CommitFile("cc", "docs/c.md", "FORBIDDEN in C", "C: lands on main after A"),
		Dispatch("d1", "make D", sub, "worktree"),
	))

	// Refuse first: the violation in D is judged, and only D.
	if !res.SubagentStopBlocked("FORBIDDEN in: docs/d.md") {
		t.Fatalf("the sub-agent was not refused for its own violation in D, or was refused for more than D:\n%s", res.Output)
	}
	c := e.Git(proj, "rev-parse", "HEAD") // main as the sub-agent's worktree was cut from it
	// The session's folders: its own repository, started at A, and the sub-agent's
	// worktree, started at the HEAD that worktree had (C) and owned by the sub-agent.
	folders := e.SessionFolders(proj, "s-015-12")
	if len(folders) != 2 {
		t.Fatalf("session folders = %+v, want the root and one sub-agent worktree", folders)
	}
	if root := folders[0]; root.Role != "root" || root.BaseRef != a || root.AgentID != "" {
		t.Fatalf("root folder = %+v, want role root started at %s", root, a)
	}
	if wt := folders[1]; wt.Role != "subagent-worktree" || wt.BaseRef != c || wt.AgentID == "" || wt.RepoID == "" {
		t.Fatalf("sub-agent folder = %+v, want a worktree owned by an agent, started at %s", wt, c)
	}

	// Pass: B and C, which the worktree started with, are never reported to the
	// sub-agent, and after the fix its Stop passes.
	blocks := e.AnySubagentBlockingErrors(proj, "s-015-12")
	if len(blocks) != 1 {
		t.Fatalf("the sub-agent was refused %d times (want exactly once, for D): %q\n%s", len(blocks), blocks, res.Output)
	}
	for _, b := range blocks {
		if strings.Contains(b, "docs/b.md") || strings.Contains(b, "docs/c.md") {
			t.Fatalf("the sub-agent was refused for commits that were not its own: %q", b)
		}
	}
}

// refusedThenFixes is a sub-agent that does `first`, and once its Stop has refused
// it (the refusal is written into its own record, which the mock reads back) does
// `fix` instead. A scenario's turns all run before the first Stop, so a fix that
// has to FOLLOW a refusal cannot be an ordinary turn.
func refusedThenFixes(t *testing.T, first, fix harness.Scenario) string {
	t.Helper()
	dir := t.TempDir()
	one, two := filepath.Join(dir, "first.sh"), filepath.Join(dir, "fix.sh")
	if err := first.Script(one); err != nil {
		t.Fatal(err)
	}
	if err := fix.Script(two); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(dir, "sub.sh")
	body := "#!/bin/sh\nif grep -q 'FORBIDDEN in: ' \"${A10N_MOCK_SESSION_FILE:-/dev/null}\" 2>/dev/null; then\n  exec sh " + two +
		"\nfi\nexec sh " + one + "\n"
	if err := os.WriteFile(wrapper, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return wrapper
}
