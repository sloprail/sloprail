package checkstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/checkcache"
)

func openTestStore(t *testing.T) *store {
	t.Helper()
	s := Open(checkcache.NewMemory(), false).(*store)
	t.Cleanup(func() { s.Close() })
	return s
}

// cached and latest are the lookups of rule at hash h1, which every test run is recorded under.
func (s *store) cached(subject, kind, fingerprint string) (CachedCheck, bool, error) {
	return s.CachedCheck(rule, "h1", subject, kind, fingerprint)
}

func (s *store) latestOf(subject, kind, fingerprint string) (CachedCheck, bool, error) {
	return s.LatestCheck(rule, "h1", subject, kind, fingerprint)
}

func TestOpenReadOnly_ReadsWhatTheWriterRecordedAndCannotWrite(t *testing.T) {
	backend := checkcache.NewMemory()
	w := Open(backend, false)
	id, err := w.RecordRun(run("h0"))
	require.NoError(t, err)
	_, err = w.RecordCheck(id, judge("pass", "fp1"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	ro := Open(backend, true)
	defer ro.Close()
	_, hit, err := ro.CachedCheck(rule, "h1", "changeset", "check[1]:judge:./rubric.md.j2", "fp1")
	require.NoError(t, err)
	assert.True(t, hit)
	_, err = ro.RecordRun(run("h1"))
	assert.Error(t, err, "a read-only store cannot record")
}
