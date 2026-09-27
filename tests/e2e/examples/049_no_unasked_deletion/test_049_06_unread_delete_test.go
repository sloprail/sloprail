package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// T049_19: an `rm -rf` of memories cited with UNRELATED words is not admitted
// unjudged, whatever the engine could read. Two layouts:
//
//   - a 9 MiB asset sorting first beside a small memory: the engine's read
//     budget used to be spent by the asset, the memory reached the guard
//     unread (oldContent ""), the prepare saw "nothing removed" and skipped the
//     judge — and the citation, resolving to the user's words, admitted it.
//     Now the small memory is read and judged.
//   - a memory itself larger than a delete read: it is never read
//     (oldContentKnown false), and the prepare must send it to the judge
//     rather than read the empty bytes as nothing removed.
//
// The judge refuses (the cited words ask for something else), so each delete
// must be refused and every memory survive.
func TestT049_19_AnUnreadDeleteIsJudged(t *testing.T) {
	for _, tc := range []struct {
		name string
		lay  func(t *testing.T, e *env, proj string)
	}{
		{"a big asset sorting before a small memory", func(t *testing.T, e *env, proj string) {
			e.WriteFile(proj, "memories/topic.md", "a fact worth keeping\n")
			sparse(t, filepath.Join(proj, "memories", "0big.bin"), 9<<20)
		}},
		{"a memory too large to read", func(t *testing.T, e *env, proj string) {
			e.WriteFile(proj, "memories/topic.md", "")
			sparse(t, filepath.Join(proj, "memories", "topic.md"), 9<<20)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			proj := nudProject(t, e)
			e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR049 the cited words do not ask to delete the memories"}`)
			tc.lay(t, e, proj)

			const prompt = "tidy up the build folder"
			res := e.Run(proj, "s-049-19", prompt, Turns("done",
				Bash("d1", "sr-session trajectory cite "+shq(prompt)+" --source-types user && rm -rf memories"),
			))
			if !res.Refused() || !res.Saw("SR049 the cited words do not ask") {
				t.Errorf("the cited-but-unrelated rm -rf was not judged:\n%s", res.Output)
			}
			if !e.Exists(proj, "memories/topic.md") {
				t.Errorf("the memory is gone: the delete was admitted unjudged")
			}
		})
	}
}

// sparse makes a file of the given size without writing its bytes.
func sparse(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
