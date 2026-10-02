package checkcache

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func key(fp string) Key {
	return Key{Rule: "file-guard/x", RuleHash: "h", Kind: "check[0]:judge:./r.md.j2", Subject: "changeset", Fingerprint: fp}
}

func TestKey_EveryPartIsPartOfTheIdentity(t *testing.T) {
	base := key("f")
	for name, k := range map[string]Key{
		"rule":        {Rule: "file-guard/y", RuleHash: "h", Kind: base.Kind, Subject: base.Subject, Fingerprint: "f"},
		"rule hash":   {Rule: base.Rule, RuleHash: "h2", Kind: base.Kind, Subject: base.Subject, Fingerprint: "f"},
		"kind":        {Rule: base.Rule, RuleHash: "h", Kind: "check[1]", Subject: base.Subject, Fingerprint: "f"},
		"subject":     {Rule: base.Rule, RuleHash: "h", Kind: base.Kind, Subject: "a.go", Fingerprint: "f"},
		"fingerprint": key("g"),
	} {
		assert.NotEqual(t, base.ID(), k.ID(), name)
	}
	// Parts cannot be re-cut into one another.
	assert.NotEqual(t, Key{Rule: "ab", RuleHash: "c"}.ID(), Key{Rule: "a", RuleHash: "bc"}.ID())
}

func TestMemory_PassIsAHitAndFailIsNot(t *testing.T) {
	m := NewMemory()
	require.NoError(t, m.Put([]Result{
		{Key: key("a"), Status: StatusPass},
		{Key: key("b"), Status: StatusFail, Reasoning: "too long"},
	}))
	got, err := m.Lookup([]Key{key("a"), key("b"), key("c")})
	require.NoError(t, err)
	assert.Len(t, got, 2, "an unknown key is absent")
	assert.True(t, got[key("a").ID()].Hit())
	assert.False(t, got[key("b").ID()].Hit())
	assert.Equal(t, "too long", got[key("b").ID()].Reasoning)
}

func TestMemory_TheLatestResultOfAKeyWins(t *testing.T) {
	m := NewMemory()
	require.NoError(t, m.Put([]Result{{Key: key("a"), Status: StatusFail, Prov: Provenance{At: "2026-01-02"}}}))
	require.NoError(t, m.Put([]Result{{Key: key("a"), Status: StatusPass, Prov: Provenance{At: "2026-01-03"}}}))
	require.NoError(t, m.Put([]Result{{Key: key("a"), Status: StatusFail, Prov: Provenance{At: "2026-01-01"}}}))
	got, _ := m.Lookup([]Key{key("a")})
	assert.Equal(t, StatusPass, got[key("a").ID()].Status)
}
