package sessionstate

import (
	"database/sql"
	"errors"
	"fmt"
)

// State reads what one guardrail stored under one key.
//
// The guardrail is a parameter here, and for a read of one key it is never the
// caller's to name at the command boundary: the engine ran the hook and knows
// which rule is asking. get and set stay caller-scoped for exactly the reason
// below — a rule able to name another rule's entries for a get could read state
// it was never told about and then depend on when that rule ran.
//
// The qualification is ListStateOwned, and only that: a caller MAY name another
// guardrail as the owner of a LIST it wants to read. The worry the isolation
// guarded against was a rule reading another's state "and then depend[ing] on
// WHEN that rule ran"; that ordering worry is handled by the separate `require:
// [{context}]` mechanism (internal/dispatch/require.go checkContext), which
// guarantees the named context entered this cycle before the reading gate runs.
// So a cross-guardrail read composes with `require` — the read gets the entries,
// `require` gets the ordering — and neither get nor set is opened by it.
func (s *store) State(guardrail, key string) (string, bool, error) {
	db, err := s.conn()
	if err != nil {
		return "", false, err
	}
	var value string
	err = db.QueryRow(`
		SELECT value FROM guardrail_state
		WHERE guardrail = ? AND key = ?`, guardrail, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("sessionstate: read state %q/%q: %w", guardrail, key, err)
	}
	return value, true, nil
}

// SetState stores a value under a key, replacing rather than merging.
//
// Every merge policy is a guess about what the rule meant — whether a nested
// object is combined or overwritten, a list appended or replaced, an explicit
// null removing a field or setting one — and a rule wanting the other answer
// could not get it. A rule that wants to merge composes with the tool it
// already reaches for; a rule updating one part of what it knows writes a
// narrower key, and then nothing needs merging at all.
//
// The value is stored as given. The engine never looks inside: it could not
// tell an abandoned key from a deliberate one, so it does not sweep, does not
// expire, and does not validate.
func (s *store) SetState(guardrail, key, value string) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO guardrail_state (guardrail, key, value) VALUES (?, ?, ?)
		ON CONFLICT (guardrail, key) DO UPDATE SET value = excluded.value`,
		guardrail, key, value)
	if err != nil {
		return fmt.Errorf("sessionstate: write state %q/%q: %w", guardrail, key, err)
	}
	return nil
}

// ListState returns every entry this guardrail stored whose key begins with
// prefix, ordered by key.
//
// This is what makes per-entry storage workable: a rule holding many subjects
// writes each independently and gets the group back by asking for the prefix
// they share, instead of keeping an index of its own keys. An empty prefix is
// everything the guardrail stored.
//
// The guardrail passed here is the CALLER's own — the CLI supplies it from the
// engine-set environment, never from an argument. A caller that means to read a
// DIFFERENT guardrail's list, on purpose, calls ListStateOwned instead; that
// method is where the cross-guardrail read lives, so this one stays the plain
// "my own entries" read and its meaning does not shift under a caller.
func (s *store) ListState(guardrail, prefix string) ([]Entry, error) {
	return s.listState(guardrail, prefix)
}

// ListStateOwned returns every entry OWNER stored whose key begins with prefix,
// ordered by key — the one read where the guardrail named is not the caller's
// own.
//
// It reads exactly what ListState reads, for a guardrail the caller named on
// purpose, and is a read only: there is no owned counterpart to State or
// SetState, so this opens no way to read another rule's single key nor to write
// into another rule's keyspace. That asymmetry is the whole point — a gate that
// cross-references a sibling context's registry needs to SEE the group that
// context accumulated, and nothing more.
//
// Why naming another guardrail is safe here when get/set forbid it: the
// isolation's stated worry (see State) was a rule reading another's state "and
// then depend[ing] on WHEN that rule ran". That correctness-across-time concern
// is not this method's to solve — it is the caller's, via `require: [{context:
// <owner>}]` (internal/dispatch/require.go checkContext), which guarantees the
// owner context entered THIS cycle before the reading gate's check runs. The two
// compose: `require` establishes the ordering, this read returns the entries the
// ordering makes meaningful. A gate that reads an owner's registry without also
// declaring `require` on it is reading a possibly-stale or empty group — a
// caller mistake this layer cannot and does not police, exactly as it does not
// police what a rule does with its own list.
//
// This is a read WITHIN one session and one workspace. The database is the
// caller's own — the CLI resolves its path from the engine-set session and
// workspace, never from any argument — and owner selects only the guardrail
// column within it. So naming an owner reaches another RULE's rows in the same
// session's database and can reach nothing in another session's or another
// workspace's.
func (s *store) ListStateOwned(owner, prefix string) ([]Entry, error) {
	return s.listState(owner, prefix)
}

// listState is the shared read behind ListState and ListStateOwned: every entry
// the named guardrail stored under prefix, ordered by key. The two public
// methods differ only in whether the name is the caller's own or one it named
// on purpose; the row selection is identical, so it lives here once.
func (s *store) listState(guardrail, prefix string) ([]Entry, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	// GLOB and LIKE both read characters in the pattern that a rule's key may
	// legitimately contain, so the prefix is compared as a range instead: every
	// key that sorts at or after the prefix and before its upper bound begins
	// with it, whatever it is made of.
	//
	// The guard tests the BOUND, not the prefix. An empty bound means there is
	// no key above the range — true of the empty prefix and of an all-0xFF one
	// — and in both cases the lower bound alone is the whole answer. Testing
	// the prefix instead reads an all-0xFF prefix as bounded by "", which no
	// key sorts below, and silently returns nothing.
	bound := prefixUpperBound(prefix)
	rows, err := db.Query(`
		SELECT key, value FROM guardrail_state
		WHERE guardrail = ? AND key >= ? AND (? = '' OR key < ?)
		ORDER BY key`, guardrail, prefix, bound, bound)
	if err != nil {
		return nil, fmt.Errorf("sessionstate: list state %q/%q*: %w", guardrail, prefix, err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Key, &e.Value); err != nil {
			return nil, fmt.Errorf("sessionstate: list state %q/%q*: %w", guardrail, prefix, err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sessionstate: list state %q/%q*: %w", guardrail, prefix, err)
	}
	return entries, nil
}

// prefixUpperBound is the first string that sorts after every string beginning
// with prefix: the prefix with its last byte raised by one.
//
// Two inputs have no such bound: the empty prefix, and one whose bytes are all
// 0xFF. Both return "", which means "no upper bound" and NOT "the empty string"
// — a caller comparing keys against it directly would exclude everything, since
// no key sorts below "". A caller must test the returned bound for emptiness
// and drop the upper comparison when it is empty; testing the prefix instead
// gets the empty case right and the all-0xFF case exactly backwards.
func prefixUpperBound(prefix string) string {
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xFF {
			b[i]++
			return string(b[:i+1])
		}
	}
	return ""
}
