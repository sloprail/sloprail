package checkcache

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func idsOf(rs []Run) map[string]bool {
	m := map[string]bool{}
	for _, r := range rs {
		m[r.ID] = true
	}
	return m
}

// Runs is memoized per tip: every write that moves the ref (Put, Gc, Pull, Sync) must show in
// the next Runs of the SAME store, never the runs it held before.
func TestRunsMemoFollowsEveryTipMove(t *testing.T) {
	remote := bareRemote(t)
	s := newRepoOpt(t, Options{Remote: remote, NoAutoGc: true})

	got, err := s.Runs()
	require.NoError(t, err)
	assert.Empty(t, got)

	require.NoError(t, s.Put(genRuns(1, 3)))
	got, err = s.Runs()
	require.NoError(t, err)
	require.Len(t, got, 3)
	got, err = s.Runs() // served from the memo
	require.NoError(t, err)
	require.Len(t, got, 3)

	require.NoError(t, s.Put(genRuns(2, 2)))
	got, err = s.Runs()
	require.NoError(t, err)
	assert.Len(t, got, 5, "a Put moves the tip: the memo must not hide its runs")

	_, err = s.Gc()
	require.NoError(t, err)
	got, err = s.Runs()
	require.NoError(t, err)
	assert.Len(t, got, 5, "a Gc keeps every run, and Runs agrees with a fresh store")
	fresh, err := Open(Options{Dir: s.opt.Dir, Remote: remote, NoAutoGc: true})
	require.NoError(t, err)
	want, err := fresh.Runs()
	require.NoError(t, err)
	assert.Equal(t, idsOf(want), idsOf(got))

	// another machine stores a run; this store sees it only after Pull (the tip moves then)
	other := newRepoOpt(t, Options{Remote: remote, NoAutoGc: true})
	require.NoError(t, other.Pull())
	third := genRuns(3, 1)
	require.NoError(t, other.Put(third))

	got, err = s.Runs()
	require.NoError(t, err)
	require.Len(t, got, 5, "premise: nothing pulled yet")
	require.NoError(t, s.Pull())
	got, err = s.Runs()
	require.NoError(t, err)
	assert.Len(t, got, 6, "a Pull moves the tip: the pulled run must be listed")
	assert.True(t, idsOf(got)[third[0].ID])

	require.NoError(t, other.Put(genRuns(4, 1)))
	require.NoError(t, s.Sync())
	got, err = s.Runs()
	require.NoError(t, err)
	assert.Len(t, got, 7, "a Sync moves the tip: the pulled run must be listed")
}

// A second Store on the same repository (another process) moves the ref under this one: the
// tip is read each call, so the memo can never outlive it.
func TestRunsMemoSeesAWriteFromAnotherStoreOnTheSameRepo(t *testing.T) {
	a := newRepo(t, "")
	require.NoError(t, a.Put(genRuns(1, 2)))
	got, err := a.Runs()
	require.NoError(t, err)
	require.Len(t, got, 2)

	b, err := Open(Options{Dir: a.opt.Dir})
	require.NoError(t, err)
	require.NoError(t, b.Put(genRuns(2, 1)))

	got, err = a.Runs()
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

// The memo is the store's: a caller that reorders or overwrites what Runs returned must not
// change what the next Runs returns.
func TestRunsReturnsACopyOfTheMemo(t *testing.T) {
	s := newRepo(t, "")
	require.NoError(t, s.Put(genRuns(1, 4)))
	first, err := s.Runs()
	require.NoError(t, err)
	require.Len(t, first, 4)
	ids := make([]string, len(first))
	for i, r := range first {
		ids[i] = r.ID
	}

	for i := range first {
		first[i] = Run{ID: "scribble"}
	}

	again, err := s.Runs()
	require.NoError(t, err)
	require.Len(t, again, 4)
	for i, r := range again {
		assert.Equal(t, ids[i], r.ID, "position %d", i)
	}

	again[0], again[3] = again[3], again[0] // a sort by the caller
	third, err := s.Runs()
	require.NoError(t, err)
	assert.Equal(t, ids[0], third[0].ID)
	assert.Equal(t, ids[3], third[3].ID)
}

// Parallel readers and a writer on one store: run under -race.
func TestRunsMemoIsRaceFree(t *testing.T) {
	s := newRepoOpt(t, Options{NoAutoGc: true})
	require.NoError(t, s.Put(genRuns(1, 3)))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				rs, err := s.Runs()
				if err != nil {
					t.Error(err)
					return
				}
				if len(rs) >= 1 {
					rs[0] = Run{} // the copy is the caller's
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 3; i++ {
			if err := s.Put(genRuns(int64(10+i), 1)); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
	rs, err := s.Runs()
	require.NoError(t, err)
	assert.Len(t, rs, 6)
	for _, r := range rs {
		assert.NotEmpty(t, r.ID, "no caller's scribble reached the memo")
	}
}
