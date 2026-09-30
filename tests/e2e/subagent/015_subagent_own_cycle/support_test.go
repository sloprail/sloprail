package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A sub-agent's OWN cycle, observed end to end through a user's own wiring.
//
// # Why this package exists, and what it overturns
//
// 013 and 014 both record that a sub-agent's own cycle cannot be asserted on
// from an e2e, and 014 states the reason as a measurement:
//
//	"An isolated sub-agent's tool calls are never APPLIED. […] the bound
//	 worktree […] is EMPTY — the file appears in no tree at all. So a
//	 worktree-isolated sub-agent produces no tree difference, its Post cycle
//	 has nothing to judge, and no guardrail can fire in it however correct the
//	 engine is. A test asserting 'the sub-agent's own file was judged' is
//	 therefore unwritable here, not merely awkward: one was written against
//	 this and removed rather than weakened into something that passes."
//
// That measurement is WRONG, and re-measuring it is what this package is built
// on. It is true of the Write tool and false of Bash, and the two notes above
// generalised from the one to all tool calls. Measured here, and pinned by the
// tests below rather than asserted in a comment:
//
//   - An isolated sub-agent's Bash DOES execute, and it executes INSIDE the
//     bound worktree. A probe recorded its `pwd` as
//     .../.claude/worktrees/agent-<id>, and the file it created is in that
//     worktree and in no other tree.
//   - So the sub-agent's Post cycle has a real tree difference to judge, and a
//     guardrail DOES fire in it — the worktree carries its own checkout of
//     .sloprail/guardrails, so the rule the project declares is live there.
//   - And it fires under the SUB-AGENT'S OWN session identity, which is not the
//     root's.
//
// The Write tool is genuinely never applied, which is what the earlier probes
// used. That is why the limit looked total: every scenario in 013 and 014 that
// tried to observe a sub-agent's effects used Write. Bash is the channel the
// mock executes, and 013_06 already relies on this for the SHARED-tree case —
// the isolated case was simply never retried with it.
//
// What follows from the correction is the substance of this package: two of the
// four invariants that 014 declares unobservable are observable, and are
// asserted here against a user's own wiring rather than only at unit level.
//
//	judged_on_its_own_record    — T015_01, T015_02. The sub-agent's cycle judges
//	                              the file IT made, in the tree IT was bound to,
//	                              and the root's cycle does not see it.
//	subagent_state_is_its_own   — T015_03, T015_04. What a sub-agent's guardrail
//	                              stores is not readable by the parent's, and two
//	                              sub-agents do not read each other's.
//
// The other two remain where 014 puts them. the_event_says_which is a claim
// about which command the plugin binds, covered by T013_01/T013_05; it has no
// observable consequence to separate it from its negation at this level beyond
// what those already assert. identity_comes_from_the_hook is asserted here in
// its observable half — the identity a sub-agent's hook is handed is not the
// parent's — but its "never from the agent itself" half is a statement about an
// absence in the environment, and is pinned at unit level.
//
// # RE-VEHICLED onto the NEW file-guard nature (was old GUARDRAIL.md hooks)
//
// A sub-agent's OWN cycle is a Post cycle: the sub-agent's work has settled in its
// tree, and the guardrail fires at the sub-agent's SubagentStop against that
// difference. That is exactly a file-guard's after-check — a
// file-guard fires on the settled Post file event and records into the same
// revalidation store the old format used (services/sr-session/nature_fileguard.go's
// runFileGuardsPost, driven from the SubagentStop path the same as the root's Stop).
// So every observation this package rests on — that the sub-agent's cycle judges the
// file IT made, under the SUB-AGENT'S own SR_SESSION_ID, and that its state does not
// pool with the parent's — is reached identically through the file-guard's own
// after-check. The exact transformation is in tests/e2e/REVEHICLE-PATTERN.md.
//
// The rules here observe the sub-agent's own file EVENTS and its own SR_SESSION_ID,
// which the file-guard check is handed the same way the old hook was. `match:
// "**/*.md"` selects the sub-agent's `.md` work at any depth (all of it lands as
// `.md`), and never matches the guard's own ledger (`log`, `count` — no `.md`
// suffix) so no self-observation doubles the ledger. Refusals (T015_07, T015_08)
// still surface at SubagentStop and are read with e.SubagentBlockingErrors, from
// the sub-agent's own record, where real Claude Code writes them.
//
// # The ledger channel, and a trap that cost real time
//
// A guardrail check here writes to "$SR_GUARDRAIL_DIR/log" (the folder the engine
// sets for a file-guard check, `.sloprail/file-guard/<name>/`) and is read with
// subLedger. An earlier round of probes wrote to an ABSOLUTE path under t.TempDir()
// and recorded nothing at all — every one of them read as "the guardrail never
// fired", which is the same observation a genuinely dead engine produces. The
// check's own folder is the channel that works, and it is the one the rest of this
// tree already uses.
//
// For an ISOLATED sub-agent the folder in question is the one in ITS OWN worktree,
// not the project's, because the worktree is a separate checkout of a tree that
// contains .sloprail/. So a test reading the project's ledger for a sub-agent's
// verdict finds nothing and would conclude the opposite of the truth. subLedger
// below reads the right one.

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

// recordsPathAndSession is a file-guard whose after-check judges files created in
// a cycle, and writes down what it was asked about and WHOSE session it was asked
// as.
//
// The session identity is the load-bearing half. That a sub-agent's file reached
// a guardrail is true under an engine that scopes sub-agents properly and under
// one that judges everything as the parent — the ledger line would carry the
// same path either way. What separates them is SR_SESSION_ID, and nothing else
// on the line does; the file-guard check is handed it (SR_SESSION_ID) exactly as
// the old hook was.
//
// `match: "**/*.md"` selects the sub-agent's `.md` work at any depth and never
// its own `log` ledger (no `.md` suffix), so the guard cannot re-observe its own
// bookkeeping.
const recordsPathAndSession = `match: "**/*.md"
checks:
  - script: ./record.sh
`

const recordScript = `#!/bin/sh
payload=$(cat)
path=$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
echo "judged path=[$path] session=[$SR_SESSION_ID]" >> "$SR_GUARDRAIL_DIR/log"
exit 0
`

// readsBackItsOwnState is the file-guard the state-isolation tests rest on: it
// reports what it can read back before writing its own note.
//
// This is the only shape that can tell pooled state from separate state. A rule
// that merely WROTE would leave two stores looking alike from outside; what
// distinguishes them is whether one scope can READ what another wrote. So each
// invocation reports `before=[...]` — the value standing in ITS scope when it
// ran — and then writes its own path there.
const readsBackItsOwnState = `match: "**/*.md"
checks:
  - script: ./record.sh
`

const readsBackScript = `#!/bin/sh
payload=$(cat)
path=$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
echo "judged path=[$path] session=[$SR_SESSION_ID] before=[$(sr-session state get seen 2>&1)]" >> "$SR_GUARDRAIL_DIR/log"
sr-session state set seen "$path" >/dev/null 2>&1
exit 0
`

// sessionOf reads the session identity a ledger line recorded, or "" when the
// line carries none.
func sessionOf(line string) string { return between(line, "session=[", "]") }

// beforeOf reads what a hook could see in its own scope when it ran, or "" when
// the line carries no such field.
//
// An empty CAPTURE is a real answer and is not the same as a missing field: it
// is a scope holding nothing, which is exactly what a properly separated
// sub-agent's store looks like on its first write. Callers that need to tell the
// two apart check the line for the marker itself.
func beforeOf(line string) string { return between(line, "before=[", "]") }

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
