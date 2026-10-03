package checkcache

import "testing"

// A fork PR's verdicts live on the fork's results branch: PullFrom merges them into the
// lookup next to the base repository's, and writes to neither remote.
func TestPullFromMergesForkRecordsReadOnly(t *testing.T) {
	baseRemote, forkRemote := bareRemote(t), bareRemote(t)
	runs := genRuns(7, 4)

	baseWriter := newRepo(t, baseRemote)
	if err := baseWriter.Put(runs[:2]); err != nil {
		t.Fatal(err)
	}
	forkWriter := newRepo(t, forkRemote)
	if err := forkWriter.Put(runs[2:]); err != nil {
		t.Fatal(err)
	}
	baseTip := git(t, baseRemote, "rev-parse", "refs/heads/sloprail/checks")
	forkTip := git(t, forkRemote, "rev-parse", "refs/heads/sloprail/checks")

	ci := newRepo(t, baseRemote)
	if err := ci.Pull(); err != nil {
		t.Fatal(err)
	}
	found, err := ci.Lookup(keysOf(runs))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("before the fork is read, want the 2 base records, got %d", len(found))
	}
	if err := ci.PullFrom(forkRemote); err != nil {
		t.Fatal(err)
	}
	found, err = ci.Lookup(keysOf(runs))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 4 {
		t.Fatalf("base and fork records should both be found, got %d of 4", len(found))
	}
	if got := git(t, baseRemote, "rev-parse", "refs/heads/sloprail/checks"); got != baseTip {
		t.Fatalf("base remote moved: %s -> %s", baseTip, got)
	}
	if got := git(t, forkRemote, "rev-parse", "refs/heads/sloprail/checks"); got != forkTip {
		t.Fatalf("fork remote moved: %s -> %s", forkTip, got)
	}
}

func TestPullFromForkWithoutBranchIsNotAnError(t *testing.T) {
	ci := newRepo(t, "")
	if err := ci.PullFrom(bareRemote(t)); err != nil {
		t.Fatalf("a fork that never ran sr-checks is not an error: %v", err)
	}
	if err := ci.PullFrom(t.TempDir() + "/nowhere"); err == nil {
		t.Fatal("an unreachable fork should be an error")
	}
	if err := ci.PullFrom(""); err != nil {
		t.Fatal(err)
	}
}
