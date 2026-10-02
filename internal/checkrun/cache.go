package checkrun

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/sloprail/sloprail/internal/checkcache"
)

// OpenCache is the repository's check cache: the orphan branch `sloprail/checks`, read from
// and published to `origin` when the repository has one (local only otherwise). Everything
// goes through git plumbing; nothing is checked out. The remote is synced first, so results
// another machine pushed are found; a remote that cannot be reached is reported on w and the
// local copy is used.
func OpenCache(w io.Writer, root string) (*checkcache.Store, error) {
	opt := checkcache.Options{Dir: root}
	if out, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output(); err == nil && strings.TrimSpace(string(out)) != "" {
		opt.Remote = "origin"
	}
	store, err := checkcache.Open(opt)
	if err != nil {
		return nil, fmt.Errorf("sloprail: the check results could not be opened: %w", err)
	}
	if err := store.Sync(); err != nil {
		fmt.Fprintf(w, "sloprail: the check results could not be fetched from origin, using the local copy: %v\n", err)
	}
	return store, nil
}
