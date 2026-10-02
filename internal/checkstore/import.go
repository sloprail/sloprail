package checkstore

import (
	"fmt"

	"github.com/sloprail/sloprail/internal/checkcache"
)

// Import copies every run, check and item of the sqlite check-results database at src — the
// checks.db an older engine kept — into dst, tagging each run with agentID when the source
// says none: the migration path from the old store to the cache, and the rows an older engine
// kept in a sub-agent's own database. Idempotent (a run keeps its id and is skipped when dst
// already holds it), and src is only read: it is left in place, untouched. Returns how many
// runs were new to dst. dst must be a store opened by this package; the runs are written to
// its cache when it is closed.
func Import(dst Store, src, agentID string) (int, error) {
	s, ok := dst.(*store)
	if !ok {
		return 0, fmt.Errorf("checkstore: Import needs a store opened by this package")
	}
	s.mu.Lock()
	err := s.writable()
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	runs, err := checkcache.ReadLegacyDB(src, agentID)
	if err != nil {
		return 0, err
	}
	known, err := s.allRuns()
	if err != nil {
		return 0, err
	}
	have := map[string]bool{}
	for _, r := range known {
		have[r.ID] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range runs {
		if have[r.ID] {
			continue
		}
		r := r
		s.runs = append(s.runs, &r)
		s.byID[r.ID] = &r
		n++
	}
	return n, nil
}
