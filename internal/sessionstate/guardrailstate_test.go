package sessionstate

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestState_AbsentKeyIsNotAnError(t *testing.T) {
	// A rule asking whether it has seen something before should not have to
	// tell "no" apart from "broken".
	s := openTestStore(t)

	value, found, err := s.State("refactor", "never-written")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, value)
}

func TestSetState_RoundTrips(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.SetState("refactor", "pending/a.go", `{"declared":true}`))

	value, found, err := s.State("refactor", "pending/a.go")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, `{"declared":true}`, value)
}

func TestSetState_ReplacesRatherThanMerges(t *testing.T) {
	// Every merge policy is a guess about what the rule meant. A rule that
	// wants to merge composes; the engine just replaces.
	s := openTestStore(t)
	require.NoError(t, s.SetState("refactor", "k", `{"a":1,"b":2}`))
	require.NoError(t, s.SetState("refactor", "k", `{"a":9}`))

	value, _, err := s.State("refactor", "k")
	require.NoError(t, err)
	assert.Equal(t, `{"a":9}`, value)
}

func TestSetState_ValueIsOpaque(t *testing.T) {
	// The engine stores what it is given and never looks inside — not JSON it
	// would validate, not text it would trim.
	s := openTestStore(t)
	for _, value := range []string{"", "  ", "not json at all", "{", "\n\t{\"a\": 1}\n"} {
		require.NoError(t, s.SetState("g", "k", value))
		got, found, err := s.State("g", "k")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, value, got)
	}
}

// sr:proves session/state-is-the-guardrails-own
func TestState_ScopedToItsGuardrail(t *testing.T) {
	// What makes ordering between guardrails unobservable: no rule can read
	// another's entries, so no rule can depend on when another ran.
	s := openTestStore(t)
	require.NoError(t, s.SetState("refactor", "shared-key", "mine"))
	require.NoError(t, s.SetState("memory-hook", "shared-key", "theirs"))

	mine, _, err := s.State("refactor", "shared-key")
	require.NoError(t, err)
	theirs, _, err := s.State("memory-hook", "shared-key")
	require.NoError(t, err)

	assert.Equal(t, "mine", mine)
	assert.Equal(t, "theirs", theirs)
}

func TestListState_EmptyPrefixIsEverythingThisGuardrailStored(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.SetState("refactor", "b", "2"))
	require.NoError(t, s.SetState("refactor", "a", "1"))
	require.NoError(t, s.SetState("other", "c", "3"))

	entries, err := s.ListState("refactor", "")
	require.NoError(t, err)
	assert.Equal(t, []Entry{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}, entries)
}

func TestListState_OrderedByKey(t *testing.T) {
	// The order is part of the answer: a rule listing a group gets a stable
	// sequence rather than whatever the storage happened to return.
	s := openTestStore(t)
	for _, k := range []string{"p/c", "p/a", "p/b"} {
		require.NoError(t, s.SetState("g", k, k))
	}

	entries, err := s.ListState("g", "p/")
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, []string{"p/a", "p/b", "p/c"}, keysOf(entries))
}

func TestListState_PrefixExcludesNonMatches(t *testing.T) {
	s := openTestStore(t)
	for _, k := range []string{"pending/a", "pending/b", "done/a", "pend", "qending/a"} {
		require.NoError(t, s.SetState("g", k, k))
	}

	entries, err := s.ListState("g", "pending/")
	require.NoError(t, err)
	assert.Equal(t, []string{"pending/a", "pending/b"}, keysOf(entries))
}

func TestListState_PrefixIsScopedToItsGuardrail(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.SetState("refactor", "p/a", "mine"))
	require.NoError(t, s.SetState("memory-hook", "p/b", "theirs"))

	entries, err := s.ListState("refactor", "p/")
	require.NoError(t, err)
	assert.Equal(t, []string{"p/a"}, keysOf(entries))
}

func TestListState_NoMatchesIsEmptyNotAnError(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.SetState("g", "a", "1"))

	entries, err := s.ListState("g", "zzz")
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestListState_PrefixMatchesTheKeyItself(t *testing.T) {
	// A key equal to the prefix begins with it. Excluding it would make a rule
	// that stores a group's summary at the group's own key lose it.
	s := openTestStore(t)
	require.NoError(t, s.SetState("g", "p", "summary"))
	require.NoError(t, s.SetState("g", "p/a", "member"))

	entries, err := s.ListState("g", "p")
	require.NoError(t, err)
	assert.Equal(t, []string{"p", "p/a"}, keysOf(entries))
}

func TestListState_PrefixTreatsPatternCharactersLiterally(t *testing.T) {
	// Keys are the rule's own and may contain anything. A LIKE or GLOB scan
	// would read % _ * ? [ ] as wildcards and return keys that do not begin
	// with the prefix at all.
	s := openTestStore(t)
	for _, k := range []string{"a%b/1", "axb/1", "a_b/1", "a*b/1", "azb/1", "a[b]/1", "aXb/1"} {
		require.NoError(t, s.SetState("g", k, k))
	}

	for _, prefix := range []string{"a%b/", "a_b/", "a*b/", "a[b]/"} {
		entries, err := s.ListState("g", prefix)
		require.NoError(t, err)
		assert.Equal(t, []string{prefix + "1"}, keysOf(entries), "prefix %q", prefix)
	}
}

func TestListState_PrefixHandlesHighBytes(t *testing.T) {
	// The range bound is computed by raising the prefix's last byte. When that
	// byte is 0xFF the raise carries left, and when EVERY byte is 0xFF there is
	// nothing left to carry into — the prefix has no upper bound at all.
	//
	// That last case is the one that bites: the bound comes back empty, and a
	// scan that mistakes "no upper bound" for "bounded by the empty string"
	// returns nothing while exiting zero. A rule reconciling a declared
	// refactor would read "nothing stored" and conclude the work was never
	// declared.
	cases := []struct {
		name   string
		prefix string
	}{
		{"carries out of a high byte", "k\xff"},
		{"all high bytes, one", "\xff"},
		{"all high bytes, two", "\xff\xff"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			require.NoError(t, s.SetState("g", tc.prefix+"/a", "1"))
			require.NoError(t, s.SetState("g", tc.prefix+"/b", "2"))
			require.NoError(t, s.SetState("g", tc.prefix, "self"))
			require.NoError(t, s.SetState("g", "other", "3"))

			entries, err := s.ListState("g", tc.prefix)
			require.NoError(t, err)
			assert.Equal(t, []string{tc.prefix, tc.prefix + "/a", tc.prefix + "/b"}, keysOf(entries))
		})
	}
}

func TestListState_MatchesHasPrefixOverTheWholeKeyspace(t *testing.T) {
	// The property the range exists to satisfy, run through the database
	// rather than against a Go re-statement of the predicate. The earlier
	// version of this test checked the bound arithmetic in Go and never
	// queried anything, so it passed while the SQL bound the wrong parameter
	// and dropped every all-0xFF group.
	keys := []string{
		"", "a", "ab", "abc", "b", "p", "p/", "p/a", "p0",
		"pending/x", "pending/y", "a%b/1", "axb/1", "a_b/1", "a*b/1", "a[b]/1",
		"k\xff", "k\xff/a", "\xff", "\xff/a", "\xff\xff", "\xff\xff/a", "\xfe",
	}
	prefixes := append([]string{"", "zzz", "\xff\xff\xff"}, keys...)

	s := openTestStore(t)
	for _, k := range keys {
		require.NoError(t, s.SetState("g", k, "v"))
	}

	for _, prefix := range prefixes {
		var want []string
		for _, k := range keys {
			if strings.HasPrefix(k, prefix) {
				want = append(want, k)
			}
		}
		sort.Strings(want)

		entries, err := s.ListState("g", prefix)
		require.NoError(t, err, "prefix %q", prefix)
		assert.Equal(t, want, keysOf(entries), "prefix %q", prefix)
	}
}

func TestListStateOwned_ReadsAnotherGuardrailsEntries(t *testing.T) {
	// The whole point of the owned read: a caller names a DIFFERENT guardrail
	// and gets that guardrail's entries. This is the cross-guardrail read
	// ListState deliberately does not do.
	s := openTestStore(t)
	require.NoError(t, s.SetState("scanner-declared", "scanner:a", `{"kw":["x"]}`))
	require.NoError(t, s.SetState("scanner-declared", "scanner:b", `{"kw":["y"]}`))
	require.NoError(t, s.SetState("gate", "own:thing", "gate-value"))

	entries, err := s.ListStateOwned("scanner-declared", "")
	require.NoError(t, err)
	assert.Equal(t, []Entry{
		{Key: "scanner:a", Value: `{"kw":["x"]}`},
		{Key: "scanner:b", Value: `{"kw":["y"]}`},
	}, entries)
}

func TestListStateOwned_AppliesThePrefixRange(t *testing.T) {
	// The owned read is the same read as ListState for a named owner: the prefix
	// is the same byte range, matching the owner's keys under the prefix and no
	// others. A cross-guardrail read must not quietly become an all-keys read.
	s := openTestStore(t)
	for _, k := range []string{"scanner:a", "scanner:b", "other:c", "scanne", "scannes:z"} {
		require.NoError(t, s.SetState("owner", k, k))
	}

	entries, err := s.ListStateOwned("owner", "scanner:")
	require.NoError(t, err)
	assert.Equal(t, []string{"scanner:a", "scanner:b"}, keysOf(entries))
}

func TestListStateOwned_MatchesListStateForTheSameGuardrail(t *testing.T) {
	// Owned and own are one read differing only in whose name is passed: reading
	// a guardrail's own list through ListStateOwned returns exactly what
	// ListState returns for it. If the two ever diverge, the split has grown a
	// second behaviour it was not meant to have.
	s := openTestStore(t)
	for _, k := range []string{"p/c", "p/a", "p/b", "q/a"} {
		require.NoError(t, s.SetState("g", k, k))
	}

	own, err := s.ListState("g", "p/")
	require.NoError(t, err)
	owned, err := s.ListStateOwned("g", "p/")
	require.NoError(t, err)
	assert.Equal(t, own, owned)
}

func TestListStateOwned_OwnerWithNoEntriesIsEmptyNotAnError(t *testing.T) {
	// A gate may name a context that never wrote anything — the context did not
	// enter this cycle, or entered and had nothing to log. That is an empty
	// answer, not a failure: the gate decides what an empty registry means, and
	// it cannot decide if the read errored instead.
	s := openTestStore(t)
	require.NoError(t, s.SetState("someone-else", "k", "v"))

	entries, err := s.ListStateOwned("never-wrote", "")
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestListStateOwned_DoesNotDisturbTheCallersOwnEntries(t *testing.T) {
	// Reading an owner's list is a read: the caller's own entries are unchanged
	// after it, and the caller still reads its own — not the owner's — through
	// its own-scoped methods.
	s := openTestStore(t)
	require.NoError(t, s.SetState("owner", "shared", "owner-value"))
	require.NoError(t, s.SetState("caller", "shared", "caller-value"))

	_, err := s.ListStateOwned("owner", "")
	require.NoError(t, err)

	// The caller's own read still sees the caller's value, not the owner's.
	got, found, err := s.State("caller", "shared")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "caller-value", got)

	callerList, err := s.ListState("caller", "")
	require.NoError(t, err)
	assert.Equal(t, []Entry{{Key: "shared", Value: "caller-value"}}, callerList)
}

// sr:proves session/state-is-the-guardrails-own
func TestListStateOwned_IsReadOnly_NoOwnedSetExists(t *testing.T) {
	// The owned read opens no owned WRITE. There is no ListStateOwned
	// counterpart for set: a caller can read another guardrail's list, and there
	// is no method on the store by which it writes into another's keyspace. This
	// pins that asymmetry — set stays caller-scoped, taking the guardrail the CLI
	// supplies from the environment.
	//
	// The store's only writer is SetState, which writes under the guardrail it is
	// given. So writing "as owner" would require the CLI to hand SetState the
	// owner's name, which it never does — there is no --owner on set. Here we
	// prove the store keeps the two guardrails' keyspaces separate under a write:
	// a write under one name is invisible to the other's owned read.
	s := openTestStore(t)
	require.NoError(t, s.SetState("caller", "k", "caller-wrote-this"))

	// The caller wrote under its own name; the owner's list is untouched by it.
	ownerList, err := s.ListStateOwned("owner", "")
	require.NoError(t, err)
	assert.Empty(t, ownerList, "a write under the caller's name must not appear under the owner")
}

func TestPrefixUpperBound(t *testing.T) {
	cases := []struct{ prefix, want string }{
		{"", ""},
		{"a", "b"},
		{"az", "a{"}, // '{' is 'z'+1
		{"p/", "p0"},
		{"\xff", ""},
		{"a\xff", "b"},
		{"\xff\xff", ""},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, prefixUpperBound(tc.prefix), "prefix %q", tc.prefix)
	}
}

func TestPrefixUpperBound_BoundsExactlyThePrefixedKeys(t *testing.T) {
	// The property the range relies on: for any key, sorting inside
	// [prefix, bound) is the same question as beginning with the prefix.
	prefixes := []string{"a", "p/", "pending/", "a%b/", "k\xff"}
	keys := []string{"", "a", "ab", "b", "p", "p/", "p/a", "p0", "pending/x", "a%b/1", "axb/1", "k\xff/a", "k\xff\xff"}

	for _, prefix := range prefixes {
		bound := prefixUpperBound(prefix)
		for _, key := range keys {
			inRange := key >= prefix && (bound == "" || key < bound)
			assert.Equal(t, strings.HasPrefix(key, prefix), inRange, "prefix %q key %q", prefix, key)
		}
	}
}

// keysOf returns nil rather than an empty slice for no entries, so a caller
// comparing against a nil expectation is comparing contents rather than which
// of two empty representations each side happened to build.
func keysOf(entries []Entry) []string {
	if len(entries) == 0 {
		return nil
	}
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		keys = append(keys, e.Key)
	}
	return keys
}
