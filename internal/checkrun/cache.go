package checkrun

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/sloprail/sloprail/internal/checkcache"
	"github.com/sloprail/sloprail/internal/declaration"
)

// OpenCache is the repository's check cache: the orphan branch `sloprail/checks`, read from
// and published to `origin` when the repository has one (local only otherwise). Everything
// goes through git plumbing; nothing is checked out. The remote is fetched first, so results
// another machine pushed are found; a remote that cannot be reached is reported on w and the
// local copy is used. With write (`run`) it then also imports an older engine's stores and
// pushes what is pending; without it (`verify`, `show`) it only reads: nothing is pushed,
// migrated or written. guards are the rules in force: an older store's verdicts are re-keyed
// by them (a verdict of a rule not among them is left behind).
func OpenCache(w io.Writer, root string, write bool, guards []declaration.FileGuard) (*checkcache.Store, error) {
	opt := checkcache.Options{Dir: root}
	if out, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output(); err == nil && strings.TrimSpace(string(out)) != "" {
		opt.Remote = "origin"
	}
	store, err := checkcache.Open(opt)
	if err != nil {
		return nil, fmt.Errorf("sloprail: the check results could not be opened: %w", err)
	}
	if !write {
		// Read-only: fetch what others stored, push nothing.
		if err := store.Pull(); err != nil {
			fmt.Fprintf(w, "sloprail: the check results could not be fetched from origin, using the local copy: %v\n", err)
		}
		return store, nil
	}
	if err := store.Sync(); err != nil {
		fmt.Fprintf(w, "sloprail: the check results could not be fetched from origin, using the local copy: %v\n", err)
	}
	// Results filed under an older key schema are re-keyed once (a no-op when current), by
	// rebuilding each stored pass's key at its recorded range (RebuildKeys): nothing is judged.
	store.SetRebuild(RebuildKeys(root, guards))
	stats, done, err := store.MigrateKeys()
	if err != nil {
		fmt.Fprintf(w, "sloprail: the check results could not be re-keyed, older verdicts may be judged again: %v\n", oneLine(err))
	} else if done {
		fmt.Fprint(w, migrationLine(stats))
	}
	return store, nil
}

// OpenLocalCache is the repository's check cache without its remote: the local copy of the
// results branch only, which is what a Stop reads (nothing is fetched or pushed).
func OpenLocalCache(root string) (*checkcache.Store, error) {
	store, err := checkcache.Open(checkcache.Options{Dir: root})
	if err != nil {
		return nil, fmt.Errorf("sloprail: the check results could not be opened: %w", err)
	}
	return store, nil
}

// WarnPending says on w, in one line, that results are stored locally but not pushed (nil
// PendingPush: nothing is said). A result the remote never receives reads as "not judged"
// to CI's verify, so the writer must hear of it.
func WarnPending(w io.Writer, store *checkcache.Store) {
	if err := store.PendingPush(); err != nil {
		fmt.Fprintf(w, "sloprail: results stored locally, not pushed: %v; CI verify will say not judged until they are\n", oneLine(err))
	}
}

func oneLine(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}
