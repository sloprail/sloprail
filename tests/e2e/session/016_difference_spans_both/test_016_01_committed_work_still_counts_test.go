package e2e

import (
	"encoding/json"
	"testing"
)

// difference_spans_both: a cycle's difference covers work that has been
// committed and work that has not.
//
// The spec's reasoning: "An agent that commits during a cycle leaves a tree with
// nothing outstanding in it. A difference that only looked at what is
// outstanding would find nothing and report that the cycle changed nothing,
// which is precisely wrong."
//
// The failing implementation this guards against is the obvious one: `git
// status`, or a diff against HEAD. Both are empty immediately after a commit.
// The correct comparison is against the session's recorded point, which does not
// move when the agent commits — established on impl/baseline-mark's T005_03.

const bindPostFileEvents = `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
  PostFileUpdate:
    - hooks:
        - type: command
          command: ./record.sh
  PostFileDelete:
    - hooks:
        - type: command
          command: ./record.sh
---

# Records every after-the-fact file event it is handed
`

const recordScript = `#!/bin/sh
cat >> "$PWD/seen"
echo >> "$PWD/seen"
exit 0
`

type observed struct {
	Kind string
	Path string
}

func observedFiles(t *testing.T, lines []string) []observed {
	t.Helper()
	var got []observed
	for _, line := range lines {
		var p struct {
			Event struct {
				Kind   string         `json:"kind"`
				Fields map[string]any `json:"fields"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("hook was handed something that is not an event payload: %v\n%s", err, line)
		}
		path, _ := p.Event.Fields["path"].(string)
		got = append(got, observed{Kind: p.Event.Kind, Path: path})
	}
	return got
}

func sawPath(got []observed, path string) bool {
	for _, o := range got {
		if o.Path == path {
			return true
		}
	}
	return false
}

// T016_01: work the agent committed during the cycle is still reported.
//
// The whole cycle's output is committed, so the tree has nothing outstanding at
// the moment the difference is taken. `git status` is empty; a diff against HEAD
// is empty. Only a comparison against the session's own starting point finds
// this file — and it must, because committing is not a way to escape review.
func TestT016_01_CommittedWorkIsStillReported(t *testing.T) {
	t.Skip("blocked on impl/stop-diff-impl: dispatchPostEvents in services/sloprail/session_stop.go is still the stub (`TODO: diff the tree against the baseline, dispatch the Post events` — returns false), so no PostFile* event is ever dispatched and no binding can observe one")
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "watcher", bindPostFileEvents, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-016-01", "write and commit", Turns("done",
		Write("w1", "committed-work.md", "written then committed\n"),
		Bash("b1", "git add -A && git commit -m 'agent commit'"),
	))

	// The premise: the agent's work really was committed, so an engine looking
	// only at outstanding work genuinely has nothing to find. Without this check
	// a failure to commit would make the test pass for the wrong reason.
	//
	// Asked of the file rather than of `git status --porcelain` being empty. The
	// guardrail writes its own ledger inside the project, and that file is
	// untracked, so the tree is never wholly clean during a run — an emptiness
	// check here fails for a reason that has nothing to do with the invariant.
	// What matters is that THIS path is committed and not outstanding.
	if status := e.Git(proj, "status", "--porcelain", "--", "committed-work.md"); status != "" {
		t.Fatalf("the agent's file is still outstanding (%q), so this does not test the "+
			"committed case at all", status)
	}
	if e.Git(proj, "log", "--oneline", "--", "committed-work.md") == "" {
		t.Fatalf("the agent's file was never committed, so this does not test the committed case")
	}

	got := observedFiles(t, e.Ledger(proj, "watcher", "seen"))
	if !sawPath(got, "committed-work.md") {
		t.Fatalf("work the agent committed mid-cycle was not reported: %v — the tree is clean, "+
			"so a difference that only looked at outstanding work found nothing and called the "+
			"cycle empty; committing must not be a way out of review", got)
	}
}

// T016_02: committed and uncommitted work are reported together.
//
// "Spans both" is a claim about one difference covering two kinds of work at
// once, and an engine could satisfy T016_01 by looking ONLY at what is
// committed since the point — which loses everything still outstanding, the
// ordinary case for most cycles.
//
// So the cycle here ends with one file committed and one file not, and both must
// arrive. Neither assertion is redundant: they fail for opposite
// implementations.
func TestT016_02_CommittedAndUncommittedWorkBothArrive(t *testing.T) {
	t.Skip("blocked on impl/stop-diff-impl: dispatchPostEvents in services/sloprail/session_stop.go is still the stub (`TODO: diff the tree against the baseline, dispatch the Post events` — returns false), so no PostFile* event is ever dispatched and no binding can observe one")
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "watcher", bindPostFileEvents, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-016-02", "commit one, leave one", Turns("done",
		Write("w1", "committed.md", "this one is committed\n"),
		Bash("b1", "git add -A && git commit -m 'agent commit'"),
		Write("w2", "outstanding.md", "this one is not\n"),
	))

	// The premise: one is committed, the other is not. Asked per path, because
	// the guardrail's own ledger is untracked and would satisfy a whole-tree
	// check on its own.
	if status := e.Git(proj, "status", "--porcelain", "--", "committed.md"); status != "" {
		t.Fatalf("the file meant to be committed is still outstanding (%q)", status)
	}
	if status := e.Git(proj, "status", "--porcelain", "--", "outstanding.md"); status == "" {
		t.Fatalf("the file meant to be outstanding was committed, so the uncommitted half " +
			"of this test is not set up")
	}

	got := observedFiles(t, e.Ledger(proj, "watcher", "seen"))
	if !sawPath(got, "committed.md") {
		t.Fatalf("the committed half of the cycle's work is missing: %v — an engine looking only "+
			"at outstanding work reports this cycle as smaller than it was", got)
	}
	if !sawPath(got, "outstanding.md") {
		t.Fatalf("the uncommitted half of the cycle's work is missing: %v — an engine looking only "+
			"at commits since the point loses everything the agent has not committed, "+
			"which is most cycles", got)
	}
}
