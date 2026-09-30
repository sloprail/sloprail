package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// guardYAML is a file-guard over docs/ with one script check, with the given
// `deletions:` value ("" leaves the key out — the default). A file-guard judges
// committed changes at Stop, so it is handed a Changeset, and `deletions:` is a
// filter on the status of the files in it.
func guardYAML(deletions string) string {
	y := "match: \"docs/**\"\n"
	if deletions != "" {
		y += "deletions: " + deletions + "\n"
	}
	return y + "checks:\n  - script: ./check.sh\n"
}

// gateYAML is the PRE-write half of the same rule: a gate over docs/ whose
// triggers follow the same `deletions:` vocabulary — a file-guard's `deletions:`
// filter is a STATUS filter on what it is asked about, and a gate says the same
// thing with the events it binds to: PreFileWrite (create + update), PreFileDelete,
// or both.
//
//	skip (default)  PreFileWrite
//	include         PreFileWrite + PreFileDelete
//	only            PreFileDelete
//
// So ONE rule (a gate and a file-guard sharing a name) is observed on both paths:
// the Pre events at pre-tool and the changeset's files at Stop. A filter applied on
// only one of them shows up as a Pre line without its Changeset line, or the reverse.
func gateYAML(deletions string) string {
	const write = "  - event: PreFileWrite\n    match: event.path startsWith \"docs/\"\n"
	const del = "  - event: PreFileDelete\n    match: event.path startsWith \"docs/\"\n"
	y := "on:\n"
	switch deletions {
	case "include":
		y += write + del
	case "only":
		y += del
	default:
		y += write
	}
	return y + "checks:\n  - script: ./check.sh\n"
}

// ledgerCheck appends `<kind> <path>` for every event a gate is handed, and
// `Changeset:<status> <path>` for every file of a changeset a file-guard is handed,
// and passes. The ledger is not under docs/ and has no suffix the plugin's own
// guards read, so no guard is ever handed its own bookkeeping.
const ledgerCheck = `#!/bin/sh
payload="$(cat)"
kind="$(printf '%s' "$payload" | jq -r '.event.kind')"
if [ "$kind" = "Changeset" ]; then
  printf '%s' "$payload" | jq -r '.changeset.files[] | "Changeset:" + .status + " " + .path' >> "$SR_GUARDRAIL_DIR/ledger"
else
  path="$(printf '%s' "$payload" | jq -r '.event.path')"
  echo "$kind $path" >> "$SR_GUARDRAIL_DIR/ledger"
fi
exit 0
`

// refuseDeletesCheck refuses a delete and passes everything else — a guard that
// exists to stop files going away. Written the naive way, with no knowledge of
// `deletions:`: whether it is ever asked about a delete is the key's to decide.
const refuseDeletesCheck = `#!/bin/sh
payload="$(cat)"
kind="$(printf '%s' "$payload" | jq -r '.event.kind')"
deleted="no"
case "$kind" in
  *FileDelete) deleted="yes" ;;
  Changeset)
    if printf '%s' "$payload" | jq -e 'any(.changeset.files[]; .status == "D")' >/dev/null; then deleted="yes"; fi ;;
esac
if [ "$deleted" = "yes" ]; then
  echo '{"reason":"DELETE-REFUSED: files under docs/ are not deleted"}'
  exit 1
fi
exit 0
`

// project is a repository with docs/pinned.md and docs/notes.md COMMITTED before
// the session — a delete is in a changeset only for a file the range's base holds —
// and with the given rules (name → `deletions:` value) installed
// as a gate AND a file-guard of that name, and committed, so their own files are
// part of the baseline rather than the diff.
func project(t *testing.T, check string, rules map[string]string) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/pinned.md", "pinned\n")
	e.WriteFile(proj, "docs/notes.md", "notes v1\n")
	for name, deletions := range rules {
		e.Gate(proj, name, gateYAML(deletions), map[string]string{"check.sh": check})
		e.FileGuard(proj, name, guardYAML(deletions), map[string]string{"check.sh": check})
	}
	e.CommitAll(proj, "the project before the session")
	return e, proj
}

// editCreateDelete is the agent's work in the ledger tests: edit a tracked file,
// create a new one, delete a tracked one — one of each file change — and commit it.
func editCreateDelete() harness.Scenario {
	return Turns("done",
		Write("w1", "docs/notes.md", "notes v2\n"),
		Write("w2", "docs/new.md", "new\n"),
		Bash("b1", "rm docs/pinned.md"),
	).ThenCommit("edit, create and delete")
}

// seen reads a guard's ledger as the set of "<kind> <file>" it was handed, the
// path reduced to its docs/-relative form so the assertion does not depend on
// how a Pre event spells it.
func seen(e *harness.Env, proj, guard string) map[string]bool {
	out := map[string]bool{}
	lines := append(e.GateLedgerLines(proj, guard, "ledger"), e.FileGuardLedgerLines(proj, guard, "ledger")...)
	for _, line := range lines {
		kind, path, _ := strings.Cut(line, " ")
		if i := strings.Index(path, "docs/"); i >= 0 {
			path = path[i:]
		}
		out[kind+" "+path] = true
	}
	return out
}

var (
	writeEvents = []string{
		"PreFileUpdate docs/notes.md", "Changeset:M docs/notes.md",
		"PreFileCreate docs/new.md", "Changeset:A docs/new.md",
	}
	deleteEvents = []string{
		"PreFileDelete docs/pinned.md", "Changeset:D docs/pinned.md",
	}
)

func requireSeen(t *testing.T, got map[string]bool, guard string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !got[w] {
			t.Errorf("guard %q was not handed %q; it saw %v", guard, w, keys(got))
		}
	}
}

func requireNotSeen(t *testing.T, got map[string]bool, guard string, not ...string) {
	t.Helper()
	for _, n := range not {
		if got[n] {
			t.Errorf("guard %q was handed %q, which its deletions: value excludes; it saw %v", guard, n, keys(got))
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// T038_01: DEFAULT (no key) — the guard runs on the edit and the create, at both
// moments, and is NOT run on the delete at either. The delete goes through.
func TestT038_01_DefaultSkipsDeletes(t *testing.T) {
	e, proj := project(t, ledgerCheck, map[string]string{"watch": ""})

	res := e.Run(proj, "s-038-01", "edit, create, delete", editCreateDelete())
	if res.Refused() {
		t.Fatalf("a passing guard refused something:\n%s", res.Output)
	}
	if e.Exists(proj, "docs/pinned.md") {
		t.Fatalf("the rm never ran, so there is no delete to observe:\n%s", res.Output)
	}

	got := seen(e, proj, "watch")
	requireSeen(t, got, "watch", writeEvents...) // the positive control: the guard is live
	requireNotSeen(t, got, "watch", deleteEvents...)
}

// T038_02: `deletions: include` — the guard runs on the delete at both moments,
// AND still on the edit and the create. A sibling guard with the key left off,
// watching the same files in the same session, is the control: it is handed the
// writes and not the delete, so the difference is the key and nothing else.
func TestT038_02_IncludeSeesDeletesAndWrites(t *testing.T) {
	e, proj := project(t, ledgerCheck, map[string]string{
		"watch-include": "include",
		"watch-default": "",
	})

	res := e.Run(proj, "s-038-02", "edit, create, delete", editCreateDelete())
	if res.Refused() {
		t.Fatalf("a passing guard refused something:\n%s", res.Output)
	}

	inc := seen(e, proj, "watch-include")
	requireSeen(t, inc, "watch-include", writeEvents...)
	requireSeen(t, inc, "watch-include", deleteEvents...)

	def := seen(e, proj, "watch-default")
	requireSeen(t, def, "watch-default", writeEvents...)
	requireNotSeen(t, def, "watch-default", deleteEvents...)
}

// T038_03: `deletions: only` — the guard runs on the delete at both moments and
// on nothing else: not the edit, not the create.
func TestT038_03_OnlySeesOnlyDeletes(t *testing.T) {
	e, proj := project(t, ledgerCheck, map[string]string{"watch": "only"})

	res := e.Run(proj, "s-038-03", "edit, create, delete", editCreateDelete())
	if res.Refused() {
		t.Fatalf("a passing guard refused something:\n%s", res.Output)
	}

	got := seen(e, proj, "watch")
	requireSeen(t, got, "watch", deleteEvents...)
	requireNotSeen(t, got, "watch", writeEvents...)
	if len(got) != len(deleteEvents) {
		t.Errorf("a deletions: only guard was handed more than the delete: %v", keys(got))
	}
}

// T038_04: a GATE that refuses deletes, with `include` and with `only`, BLOCKS the
// `rm` at PreFileDelete — the file stays on disk and the gate's reason reaches the
// agent.
func TestT038_04_GateIncludeOrOnlyBlocksTheDelete(t *testing.T) {
	for _, mode := range []string{"include", "only"} {
		t.Run(mode, func(t *testing.T) {
			e, proj := project(t, refuseDeletesCheck, map[string]string{"keep-docs": mode})

			res := e.Run(proj, "s-038-04-"+mode, "delete the pinned doc", Turns("done",
				Bash("b1", "rm docs/pinned.md"),
			))
			if !res.Refused() {
				t.Fatalf("deletions: %s — the gate did not block the delete:\n%s", mode, res.Output)
			}
			if !res.Saw("DELETE-REFUSED") {
				t.Errorf("deletions: %s — the gate's reason did not reach the agent:\n%s", mode, res.Output)
			}
			if !e.Exists(proj, "docs/pinned.md") {
				t.Errorf("deletions: %s — the file is gone although the delete was refused before it ran", mode)
			}
		})
	}
}

// T038_05: the SAME refusing rule with the key left off (the default, skip) is
// never asked about the delete — by the gate (no PreFileDelete trigger) or by the
// file-guard (skip): the `rm` goes through, and nothing blocks the turn at Stop
// either.
func TestT038_05_DefaultLetsTheDeleteThrough(t *testing.T) {
	e, proj := project(t, refuseDeletesCheck, map[string]string{"keep-docs": ""})

	const sess = "s-038-05"
	res := e.Run(proj, sess, "delete the pinned doc", Turns("done",
		Bash("b1", "rm docs/pinned.md"),
	))
	if res.Refused() {
		t.Fatalf("a rule on the default deletions: skip refused a delete:\n%s", res.Output)
	}
	if e.Exists(proj, "docs/pinned.md") {
		t.Errorf("the delete did not go through")
	}
	if blocking := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocking) != 0 {
		t.Errorf("the after-check blocked the turn on the delete: %v", blocking)
	}
}

// contentCheck is a guard that validates CONTENT, written the naive way: a file
// under docs/ must say OK. It reads the committed files and refuses one that does
// not. A file-guard on the default `deletions:` is never handed a deleted file, so
// it is spared the file it would have had no content to judge.
const contentCheck = `#!/bin/sh
payload="$(cat)"
bad="$(printf '%s' "$payload" | jq -r '[.changeset.files[] | select((.newContent // "") | contains("OK") | not) | .path] | join(" ")')"
if [ -n "$bad" ]; then
  echo "{\"reason\":\"CONTENT-REFUSED: $bad must say OK\"}"
  exit 1
fi
exit 0
`

// T038_06: a file added and then deleted inside the range is not in the net change,
// so the refusal it earned ends with it. A content guard (default deletions)
// refuses a file the agent committed; the agent then deletes the file and commits
// that. The refused range never moved, so it is judged again with the delete in it,
// and the squashed change holds nothing of the file:
//
//   - the guard is not asked about the delete, so it cannot refuse it for lacking
//     content — the turn is not blocked;
//   - the refusal is not carried into later cycles;
//   - the `deletions: only` observer, whose first range passed (it selected
//     nothing), sees the delete once, in the cycle that made it.
func TestT038_06_AFileAddedAndDeletedInOneRangeIsNotJudged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "README", "a project\n")
	e.FileGuard(proj, "says-ok", guardYAML(""), map[string]string{"check.sh": contentCheck})
	e.FileGuard(proj, "observer", guardYAML("only"), map[string]string{"check.sh": ledgerCheck})
	e.CommitAll(proj, "the project before the session")

	const sess = "s-038-06"

	// Cycle one: a doc that is not OK, committed. Refused at Stop.
	e.Run(proj, sess, "write a doc", Turns("done",
		Write("w1", "docs/bad.md", "not fine\n"),
	).ThenCommit("add a doc"))
	afterFirst := e.BlockingErrorsFrom(proj, sess, "Stop")
	if !strings.Contains(strings.Join(afterFirst, "\n"), "CONTENT-REFUSED: docs/bad.md") {
		t.Fatalf("the content guard did not refuse the bad doc (blocking: %v), so there is no refusal to end", afterFirst)
	}

	// Cycle two: the agent removes the file the refusal is about, and commits that.
	e.Run(proj, sess, "remove the doc", Turns("done",
		Bash("b1", "rm docs/bad.md"),
	).ThenCommit("remove the doc"))
	if blocking := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocking) != len(afterFirst) {
		t.Fatalf("removing the refused file did not end the refusal: %v", blocking[len(afterFirst):])
	}

	// Cycle three: unrelated work.
	e.Run(proj, sess, "unrelated", Turns("done",
		Write("w2", "README", "a project, edited\n"),
	).ThenCommit("edit the readme"))
	if blocking := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocking) != len(afterFirst) {
		t.Fatalf("a cycle after the delete was blocked: %v", blocking[len(afterFirst):])
	}

	// The observer passed the first cycle (a delete-only rule selected nothing), so
	// its range began there: the delete is in its range once, in the cycle that made
	// it, and not again.
	deletes := 0
	for _, line := range e.FileGuardLedgerLines(proj, "observer", "ledger") {
		if line == "Changeset:D docs/bad.md" {
			deletes++
		}
	}
	if deletes != 1 {
		t.Errorf("the observer was handed the delete of docs/bad.md %d times, want exactly once (the cycle that deleted it)", deletes)
	}
}
