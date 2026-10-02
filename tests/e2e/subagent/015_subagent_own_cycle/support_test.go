package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A sub-agent working in its own worktree, judged where its work landed.
//
// An isolated sub-agent's Bash executes INSIDE the bound worktree
// (.../.claude/worktrees/agent-<id>), and the file it creates is in that worktree
// and in no other tree. File-guards are judged by `sr check run --base --head`
// rather than at a stop, so what this package keeps is the layout claim: run in
// the sub-agent's worktree, a rule is handed paths relative to that tree's root.
//
// What it used to assert besides — that a sub-agent's own SubagentStop judges its
// own commits under its own session identity, that its guardrail state does not
// pool with its parent's, its refusal cycle, its own range start and its folder
// registry — was file-guard behaviour at a stop, which is gone.
//
// # The ledger channel
//
// A guardrail check here writes to "$SR_GUARDRAIL_DIR/log" (the folder the engine
// sets for a file-guard check, `.sloprail/file-guard/<name>/`) and is read with
// subLedger. For a check run inside the sub-agent's worktree the folder is the one
// in THAT worktree, not the project's, because the worktree is a separate
// checkout of a tree that contains .sloprail/.

// subLedger returns the lines a file-guard's checks appended inside a SUB-AGENT'S
// OWN worktree.
//
// Separate from e.FileGuardLedgerLines because the tree is separate. A worktree is a
// fresh checkout of the project's HEAD, so it carries its own copy of
// .sloprail/file-guard/<name>/ — and a rule that fires during the sub-agent's cycle
// writes THERE, with $SR_GUARDRAIL_DIR set to the worktree's copy of the folder. The
// project's own ledger records only what the root's cycles judged.
//
// Reading the wrong one is not a near miss. It returns nothing, which reads as "the
// sub-agent's cycle judged nothing" — the exact false conclusion that had this whole
// scenario written off as untestable.
//
// An absent file is a real answer: nothing ran in that tree.
func subLedger(t *testing.T, proj, worktree, guardrail, file string) []string {
	t.Helper()
	path := filepath.Join(proj, ".claude", "worktrees", worktree,
		".sloprail", "file-guard", guardrail, file)
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read sub-agent ledger %s: %v", path, err)
	}
	var lines []string
	for _, l := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// worktrees lists the worktree directories the mock bound for this project's
// sub-agents. An absent directory means none were bound.
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

// theWorktree returns the single worktree bound in this project, failing when
// there is not exactly one.
//
// A test about a sub-agent's own cycle rests entirely on having found the right
// tree, so "which one" is never guessed. Zero means the dispatch did not isolate
// and the test is about a different arrangement than it says; more than one
// means the ledger being read belongs to an unknown sub-agent.
func theWorktree(t *testing.T, proj string) string {
	t.Helper()
	trees := worktrees(t, proj)
	if len(trees) != 1 {
		t.Fatalf("want exactly one bound worktree, found %d (%v) — a test about a sub-agent's "+
			"own cycle cannot say whose cycle it read", len(trees), trees)
	}
	return trees[0]
}

// recordsPathAndSession is a file-guard that writes down every path it was asked
// to judge (and the SR_SESSION_ID it ran under, kept for diagnosis).
//
// `match: "**/*.md"` selects the sub-agent's `.md` work at any depth and never
// its own `log` ledger (no `.md` suffix), so the guard cannot re-observe its own
// bookkeeping.
const recordsPathAndSession = `match: "**/*.md"
checks:
  - script: ./record.sh
`

// pathsOfPayload is the shell that lists every file path the rule was asked to judge
// in the Changeset payload a check is handed, one per line: `.changeset.files[]`,
// not `.changeset.others` (the rule's own files, which the commit that installed
// it puts in the range). A file-guard is evaluated once per Stop over the whole
// range, so one run of the check covers every file the range changed and the
// recorder writes one ledger line per file.
const pathsOfPayload = `printf '%s' "$payload" | jq -r '.changeset.files[].path'`

// recordScript writes one line per file in the changeset: what it was asked about
// and WHOSE session it was asked as.
const recordScript = `#!/bin/sh
payload=$(cat)
for path in $(` + pathsOfPayload + `); do
  echo "judged path=[$path] session=[$SR_SESSION_ID]" >> "$SR_GUARDRAIL_DIR/log"
done
exit 0
`

// pathOf reads the file a ledger line was about.
func pathOf(line string) string { return between(line, "path=[", "]") }

func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// lineAbout returns the ledger line recorded for a given path, and whether there
// was one.
func lineAbout(lines []string, path string) (string, bool) {
	for _, l := range lines {
		if pathOf(l) == path {
			return l, true
		}
	}
	return "", false
}

// containsPath reports whether any ledger line is about the given path.
func containsPath(lines []string, path string) bool {
	_, ok := lineAbout(lines, path)
	return ok
}

// hitRetryCap reports whether a sub-agent was driven round until the harness
// gave up, which is what a trapped delegated cycle looks like from outside.
//
// Every test in this package checks it. A refusal that cannot be satisfied turns
// "this work is refused" into "this sub-agent can never finish", and that
// failure is silent in the assertions a test would otherwise make: the run still
// produces output, and the tree still holds the work.
func hitRetryCap(output string) bool { return strings.Contains(output, "still blocked after") }

// The state-isolation tests rest on a GATE that reads back what it can see in its own
// scope before storing a note of its own. (A file-guard's checks carry no session
// identity any more — `sr check run` takes a range, not a session — so the property
// that a sub-agent's guardrail state is its own is observed through a gate, which a
// hook still runs as the agent whose tool call it judges.)
//
// This is the only shape that can tell pooled state from separate state. A rule that
// merely WROTE would leave two stores looking alike from outside; what distinguishes
// them is whether one scope can READ what another wrote. So each run reports
// `before=[...]` — the value standing in ITS scope when it ran — and then writes a
// note of its own: the .md file the command names.
const memoGate = `on:
  - event: PreCommandInvoke
checks:
  - script: ./record.sh
`

// memoScript records, for every command naming a markdown file, who it ran as and what
// that session's own scope held, then stores the file as the scope's note. The ledger is
// outside the rule's folder (a rule's hash covers its folder).
func memoScript(ledger string) string {
	return `#!/bin/sh
payload=$(cat)
path=$(printf '%s' "$payload" | grep -o '[A-Za-z0-9_-]*\.md' | head -1)
[ -n "$path" ] || exit 0
before=$(sr-session state get seen 2>&1)
echo "judged path=[$path] session=[$SR_SESSION_ID] before=[$before]" >> '` + ledger + `'
sr-session state set seen "$path" >/dev/null 2>&1
exit 0
`
}

// sessionOf reads the session identity a ledger line recorded, or "" when the line
// carries none.
func sessionOf(line string) string { return between(line, "session=[", "]") }

// beforeOf reads what a hook could see in its own scope when it ran, or "" when the
// line carries no such field. An empty CAPTURE is a real answer and is not the same as a
// missing field: it says the scope held nothing.
func beforeOf(line string) string { return between(line, "before=[", "]") }

// memoLines is the ledger's lines.
func memoLines(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
