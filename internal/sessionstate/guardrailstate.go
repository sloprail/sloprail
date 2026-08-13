package sessionstate

import (
	"database/sql"
	"errors"
	"fmt"
)

// State reads what one guardrail stored under one key.
//
// The guardrail is a parameter here and never at the command boundary: the
// engine ran the hook and knows which rule is asking. What it must not become
// is something a rule can name for itself, because a rule able to name another
// rule's entries could read state it was never told about and then depend on
// when that rule ran.
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
func (s *store) ListState(guardrail, prefix string) ([]Entry, error) {
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
