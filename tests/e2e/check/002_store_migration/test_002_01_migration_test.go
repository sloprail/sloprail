package e2e

import (
	"strings"
	"sync"
	"testing"
)

// T002_01: `sr-checks run` over an older store does not migrate it: the run starts the new
// layout beside it, asks the judge (the stored verdict is not found under the new key) and
// leaves the older directory exactly as it was; `verify` neither converts nor reads it.
func TestT002_01_RunDoesNotMigrateAnOlderStore(t *testing.T) {
	for name, o := range layouts {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.e.InstallJudgeClaudeCapturing(f.proj, promptFile, `{"pass": true, "reasoning": "fine"}`)
			f.storeRun(t, o, "r1", "api", f.base, f.head)
			if dirs := f.refDirs(t); strings.Contains(dirs, currentDir) || !strings.Contains(dirs, o.dir) {
				t.Fatalf("premise: the ref holds %q, want only %s", dirs, o.dir)
			}
			older := f.dirOid(o.dir)
			if v := f.verify(f.base, f.head); v.Code == 0 {
				t.Fatalf("verify read a verdict from a layout it does not speak:\n%s", v.Output)
			}
			if dirs := f.refDirs(t); strings.Contains(dirs, currentDir) {
				t.Fatalf("verify wrote the new layout (%q)", dirs)
			}

			// With the mocks off, so that the judge is really asked (and recorded).
			r := f.e.CheckRunRaw(f.proj, "s-default", f.base, f.head)
			if r.Code != 0 {
				t.Fatalf("run: exit %d, want 0 (judged afresh, passing):\n%s", r.Code, r.Output)
			}
			if strings.Contains(r.Output, "migrat") {
				t.Fatalf("run said something about a migration:\n%s", r.Output)
			}
			if n := f.judgeCalls(); n != 1 {
				t.Fatalf("the judge was asked %d times, want 1 (the older verdict is not read implicitly)", n)
			}
			if dirs := f.refDirs(t); !strings.Contains(dirs, currentDir) || !strings.Contains(dirs, o.dir) {
				t.Fatalf("the ref holds %q, want the new layout beside %s", dirs, o.dir)
			}
			if got := f.dirOid(o.dir); got != older {
				t.Fatalf("the older directory changed: %s -> %s", older, got)
			}
			if n := f.migrations(t); n != 0 {
				t.Fatalf("the ref holds %d migration commits, want 0", n)
			}
		})
	}
}

// T002_02: `sr-checks migrate-keys` carries an older store across with no judge call: it exits 0,
// says how many verdicts it carried, the ref holds the current directory beside the untouched
// older one, the subject's verdict is found (verify reads it, a run asks no judge), a second call
// does nothing, and the migration is one commit.
func TestT002_02_MigrateKeysCarriesAnOlderStoreWithoutJudging(t *testing.T) {
	for name, o := range layouts {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.e.InstallJudgeClaudeCapturing(f.proj, promptFile, `{"pass": false, "reasoning": "A JUDGE WAS ASKED"}`)
			f.storeRun(t, o, "r1", "api", f.base, f.head)
			older := f.dirOid(o.dir)

			m := f.migrate()
			if m.Code != 0 {
				t.Fatalf("migrate-keys: exit %d, want 0:\n%s", m.Code, m.Output)
			}
			if !strings.Contains(m.Output, "migrated 1, skipped 0") || !strings.Contains(m.Output, "migrating cache keys:") {
				t.Fatalf("migrate-keys did not report its progress and the migration:\n%s", m.Output)
			}
			if n := f.judgeCalls(); n != 0 {
				t.Fatalf("the judge was asked %d times, want 0", n)
			}
			if dirs := f.refDirs(t); !strings.Contains(dirs, currentDir) || !strings.Contains(dirs, o.dir) {
				t.Fatalf("the ref holds %q, want %s beside %s", dirs, currentDir, o.dir)
			}
			if got := f.dirOid(o.dir); got != older {
				t.Fatalf("the older directory changed: %s -> %s", older, got)
			}

			if v := f.verify(f.base, f.head); v.Code != 0 {
				t.Fatalf("verify: exit %d, want the carried verdict found:\n%s", v.Code, v.Output)
			}
			r := f.run(f.base, f.head)
			if r.Code != 0 || strings.Contains(r.Output, "migrat") {
				t.Fatalf("run: exit %d, want 0 with the carried verdict (no judge, no migration):\n%s", r.Code, r.Output)
			}

			// A second call does nothing.
			again := f.migrate()
			if again.Code != 0 || !strings.Contains(again.Output, "nothing to migrate") {
				t.Fatalf("second migrate-keys: exit %d, want 0 and nothing to do:\n%s", again.Code, again.Output)
			}
			if n := f.migrations(t); n != 1 {
				t.Fatalf("the ref holds %d migration commits, want 1", n)
			}
			if n := f.judgeCalls(); n != 0 {
				t.Fatalf("the judge was asked %d times in all, want 0", n)
			}
			if v := f.verify(f.base, f.head); v.Code != 0 {
				t.Fatalf("verify after the second call: exit %d:\n%s", v.Code, v.Output)
			}
		})
	}
}

// T002_03: with both older directories in the ref, the newest is the source: what only the
// older one holds is not carried, and what main wrote is.
func TestT002_03_TheNewestOlderDirectoryIsTheSource(t *testing.T) {
	f := newFixture(t)
	f.storeRun(t, release, "r1", "api", f.base, f.head)
	f.storeRun(t, mainSR2, "r2", "api", f.base, f.head)

	m := f.migrate()
	if m.Code != 0 {
		t.Fatalf("migrate-keys: exit %d:\n%s", m.Code, m.Output)
	}
	if !strings.Contains(m.Output, "migrated 1, skipped 0") {
		t.Fatalf("want one verdict carried, from v2026-10-07 alone:\n%s", m.Output)
	}
	if v := f.verify(f.base, f.head); v.Code != 0 {
		t.Fatalf("verify: exit %d:\n%s", v.Code, v.Output)
	}
}

// T002_04: a stored verdict whose commits are gone (pruned from the repository) is skipped and
// counted, not an error; the other verdicts are still carried, and the content of the skipped
// one is judged only when it comes back: then the judge is asked, once.
func TestT002_04_AnUnreachableRecordIsSkippedAndCounted(t *testing.T) {
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
			f.storeRun(t, o, "r2", "elsewhere", f.base, pruned)
			f.e.InstallJudgeClaudeCapturing(f.proj, promptFile, `{"pass": true, "reasoning": "fine"}`)

			m := f.migrate()
			if m.Code != 0 {
				t.Fatalf("migrate-keys: exit %d, want 0 (an unreachable record is no error):\n%s", m.Code, m.Output)
			}
			if !strings.Contains(m.Output, "migrated 1, skipped 1 (commits unreachable: 1)") {
				t.Fatalf("migrate-keys did not count the unreachable record:\n%s", m.Output)
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

// T002_05: several sessions at once. Two `migrate-keys` and a `verify` start together on the same
// older store: all succeed, one of the migrations does the work (the other waits for it and finds
// it done), the ref holds one migration commit and the new layout is written once, the carried
// verdict is found, and no judge is asked.
func TestT002_05_ConcurrentMigrationsWriteOnce(t *testing.T) {
	f := newFixture(t)
	f.e.InstallJudgeClaudeCapturing(f.proj, promptFile, `{"pass": false, "reasoning": "A JUDGE WAS ASKED"}`)
	f.storeRun(t, mainSR2, "r1", "api", f.base, f.head)

	var wg sync.WaitGroup
	outs := make([]string, 3)
	codes := make([]int, 3)
	for i := range outs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i == 2 {
				v := f.verify(f.base, f.head)
				outs[i], codes[i] = v.Output, v.Code
				return
			}
			m := f.migrate()
			outs[i], codes[i] = m.Output, m.Code
		}()
	}
	wg.Wait()

	migrated := 0
	for i := 0; i < 2; i++ {
		if codes[i] != 0 {
			t.Fatalf("migrate-keys #%d: exit %d:\n%s", i, codes[i], outs[i])
		}
		if strings.Contains(outs[i], "migrated 1, skipped 0") {
			migrated++
		} else if !strings.Contains(outs[i], "nothing to migrate") {
			t.Fatalf("migrate-keys #%d neither migrated nor found it done:\n%s", i, outs[i])
		}
	}
	if migrated != 1 {
		t.Fatalf("%d of the two migrations did the work, want exactly 1:\n%s\n---\n%s", migrated, outs[0], outs[1])
	}
	// The verify raced the migration: it saw the ref before or after it, never a broken one.
	if codes[2] != 0 && !strings.Contains(outs[2], "not judged") {
		t.Fatalf("verify during the migration: exit %d:\n%s", codes[2], outs[2])
	}
	if n := f.migrations(t); n != 1 {
		t.Fatalf("the ref holds %d migration commits, want 1", n)
	}
	if v := f.verify(f.base, f.head); v.Code != 0 {
		t.Fatalf("verify after the migrations: exit %d:\n%s", v.Code, v.Output)
	}
	if n := f.judgeCalls(); n != 0 {
		t.Fatalf("the judge was asked %d times, want 0", n)
	}
}
