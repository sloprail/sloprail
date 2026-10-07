package e2e

import (
	"strings"
	"testing"
)

// T002_01: an older store is migrated by the first run with no judge call: the run exits 0, says
// how many verdicts it carried, the ref holds the current directory, the subject's verdict is
// found (verify reads it), a second run migrates nothing, and the migration is one commit.
func TestT002_01_AnOlderStoreIsRebuiltWithoutJudging(t *testing.T) {
	for name, o := range layouts {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.e.InstallJudgeClaudeCapturing(f.proj, promptFile, `{"pass": false, "reasoning": "A JUDGE WAS ASKED"}`)
			f.storeRun(t, o, "r1", "api", f.base, f.head)
			if dirs := f.refDirs(t); strings.Contains(dirs, currentDir) || !strings.Contains(dirs, o.dir) {
				t.Fatalf("premise: the ref holds %q, want only %s", dirs, o.dir)
			}
			if r := f.verify(f.base, f.head); r.Code == 0 {
				t.Fatalf("premise: verify read a verdict from a layout it does not speak:\n%s", r.Output)
			}

			r := f.run(f.base, f.head)
			if r.Code != 0 {
				t.Fatalf("run: exit %d, want 0 (the stored verdict is carried, no judge asked):\n%s", r.Code, r.Output)
			}
			if !strings.Contains(r.Output, "migrated 1, skipped 0") {
				t.Fatalf("run did not report the migration:\n%s", r.Output)
			}
			if n := f.judgeCalls(); n != 0 {
				t.Fatalf("the judge was asked %d times, want 0", n)
			}
			if dirs := f.refDirs(t); !strings.Contains(dirs, currentDir) {
				t.Fatalf("the ref holds %q, want %s", dirs, currentDir)
			}

			v := f.verify(f.base, f.head)
			if v.Code != 0 {
				t.Fatalf("verify: exit %d, want the carried verdict found:\n%s", v.Code, v.Output)
			}

			// A second run migrates nothing and still asks no judge.
			again := f.run(f.base, f.head)
			if again.Code != 0 || strings.Contains(again.Output, "migrated") {
				t.Fatalf("second run: exit %d, want 0 and no migration:\n%s", again.Code, again.Output)
			}
			if n := f.migrations(t); n != 1 {
				t.Fatalf("the ref holds %d migration commits, want 1", n)
			}
			if n := f.judgeCalls(); n != 0 {
				t.Fatalf("the judge was asked %d times after the second run, want 0", n)
			}
			if v := f.verify(f.base, f.head); v.Code != 0 {
				t.Fatalf("verify after the second run: exit %d:\n%s", v.Code, v.Output)
			}
		})
	}
}

// T002_02: with both older directories in the ref, the newest is the source: what only the
// older one holds is not carried, and what main wrote is.
func TestT002_02_TheNewestOlderDirectoryIsTheSource(t *testing.T) {
	f := newFixture(t)
	f.storeRun(t, release, "r1", "api", f.base, f.head)
	f.storeRun(t, mainSR2, "r2", "api", f.base, f.head)

	r := f.run(f.base, f.head)
	if r.Code != 0 {
		t.Fatalf("run: exit %d:\n%s", r.Code, r.Output)
	}
	if !strings.Contains(r.Output, "migrated 1, skipped 0") {
		t.Fatalf("want one verdict carried, from v2026-10-07 alone:\n%s", r.Output)
	}
	if v := f.verify(f.base, f.head); v.Code != 0 {
		t.Fatalf("verify: exit %d:\n%s", v.Code, v.Output)
	}
}

// T002_03: a stored verdict whose commits are gone (pruned from the repository) is skipped and
// counted, not an error; the other verdicts are still carried, and the content of the skipped
// one is judged only when it comes back: then the judge is asked, once.
func TestT002_03_AnUnreachableRecordIsSkippedAndCounted(t *testing.T) {
	for name, o := range layouts {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)

			// A commit that is later pruned from the repository, with a verdict stored for it.
			f.e.Git(f.proj, "checkout", "-q", "-b", "gone", f.base)
			f.e.WriteFile(f.proj, "docs/a.md", "content that will be pruned\n")
			pruned := f.e.CommitAll(f.proj, "pruned")
			f.e.Git(f.proj, "checkout", "-q", "main")
			f.e.Git(f.proj, "branch", "-D", "gone")
			f.e.Git(f.proj, "reflog", "expire", "--expire=now", "--all")
			f.e.Git(f.proj, "gc", "-q", "--prune=now")

			f.storeRun(t, o, "r1", "api", f.base, f.head)
			f.storeRun(t, o, "r2", "api", f.base, pruned)
			f.e.InstallJudgeClaudeCapturing(f.proj, promptFile, `{"pass": true, "reasoning": "fine"}`)

			r := f.run(f.base, f.head)
			if r.Code != 0 {
				t.Fatalf("run: exit %d, want 0 (an unreachable record is no error):\n%s", r.Code, r.Output)
			}
			if !strings.Contains(r.Output, "migrated 1, skipped 1 (commits unreachable: 1)") {
				t.Fatalf("run did not count the unreachable record:\n%s", r.Output)
			}
			if n := f.judgeCalls(); n != 0 {
				t.Fatalf("the judge was asked %d times, want 0", n)
			}

			// Its content comes back as new commits: not carried, so judged (with the mocks off).
			f.e.WriteFile(f.proj, "docs/a.md", "content that will be pruned\n")
			back := f.e.CommitAll(f.proj, "the pruned content, again")
			again := f.e.CheckRunRaw(f.proj, "s-back", f.base, back)
			if again.Code != 0 {
				t.Fatalf("run over the returned content: exit %d:\n%s", again.Code, again.Output)
			}
			if n := f.judgeCalls(); n != 1 {
				t.Fatalf("the returned content was judged %d times, want 1", n)
			}
		})
	}
}
