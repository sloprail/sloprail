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
// migrated or written.
//
// A store of an older layout is not migrated here, however it is opened: the current layout
// starts empty beside it and the older one stays in the ref untouched. Carrying its verdicts
// across is MigrateKeys, an explicit command.
func OpenCache(w io.Writer, root string, write bool) (*checkcache.Store, error) {
	store, err := checkcache.Open(cacheOptions(root))
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
	return store, nil
}

// cacheOptions is the store of the repository at root: its results branch, and origin as the
// remote when there is one.
func cacheOptions(root string) checkcache.Options {
	opt := checkcache.Options{Dir: root}
	if out, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output(); err == nil && strings.TrimSpace(string(out)) != "" {
		opt.Remote = "origin"
	}
	return opt
}

// MigrateKeys carries the verdicts an older layout of the results branch holds into the current
// one (`sr-checks migrate-keys`), reporting on w. Each stored pass whose key was built differently
// has its new key rebuilt (RebuildKeys): nothing is judged. It is the only thing that rebuilds
// keys, and changes nothing when there is nothing to carry or when it was done already.
func MigrateKeys(w io.Writer, root string, guards []declaration.FileGuard) error {
	store, err := checkcache.Open(cacheOptions(root))
	if err != nil {
		return fmt.Errorf("sloprail: the check results could not be opened: %w", err)
	}
	if err := store.Sync(); err != nil {
		fmt.Fprintf(w, "sloprail: the check results could not be fetched from origin, using the local copy: %v\n", err)
	}
	store.SetRebuild(RebuildKeys(root, guards))
	stats, done, err := store.MigrateKeys()
	if err != nil {
		return fmt.Errorf("sloprail: the check results could not be re-keyed: %w", err)
	}
	if !done {
		fmt.Fprintln(w, "sloprail: nothing to migrate: no older layout of the check results is left to carry across")
		return nil
	}
	fmt.Fprint(w, migrationLine(stats))
	WarnPending(w, store)
	return nil
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
