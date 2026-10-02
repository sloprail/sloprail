package checkcache

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func key(fp string) Key {
	return Key{Rule: "file-guard/x", RuleHash: "h", Kind: "check[0]:judge:./r.md.j2", Subject: "changeset", Fingerprint: fp}
}

func run(at string, checks ...Check) Run {
	return Run{ID: "run_" + at, RunAt: at, Rule: "file-guard/x", RuleHash: "h", Complete: true, Checks: checks}
}

func judge(fp, status string) Check {
	return Check{Subject: "changeset", Kind: "check[0]:judge:./r.md.j2", Status: status, Fingerprint: fp}
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

// Both implementations keep the same contract.
func implementations(t *testing.T) map[string]Cache {
	return map[string]Cache{
		"memory": NewMemory(),
		"file":   OpenFile(filepath.Join(t.TempDir(), "results.jsonl")),
	}
}

func TestCache_ACheckIsFoundByItsKeyWithItsRun(t *testing.T) {
	for name, c := range implementations(t) {
		t.Run(name, func(t *testing.T) {
			r := run("1", judge("a", StatusPass), judge("b", StatusFail),
				Check{Subject: "changeset", Kind: "check[1]:script:./x.sh", Status: StatusPass}) // a script: no fingerprint
			r.BaseRef, r.HeadRef = "base", "head"
			require.NoError(t, c.Put([]Run{r}))

			got, err := c.Lookup([]Key{key("a"), key("b"), key("nope")})
			require.NoError(t, err)
			assert.Len(t, got, 2, "an unknown key is absent")
			assert.Equal(t, StatusPass, got[key("a").ID()].Check.Status)
			assert.Equal(t, StatusFail, got[key("b").ID()].Check.Status)
			assert.Equal(t, "head", got[key("a").ID()].Run.HeadRef)
		})
	}
}

func TestCache_TheLatestRunOfAKeyWins(t *testing.T) {
	for name, c := range implementations(t) {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, c.Put([]Run{run("2", judge("a", StatusFail))}))
			require.NoError(t, c.Put([]Run{run("3", judge("a", StatusPass))}))
			require.NoError(t, c.Put([]Run{run("1", judge("a", StatusFail))}))
			got, _ := c.Lookup([]Key{key("a")})
			assert.Equal(t, StatusPass, got[key("a").ID()].Check.Status)
		})
	}
}

func TestCache_OnlyAFinishedPassOrFailIsFindable(t *testing.T) {
	for name, c := range implementations(t) {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, c.Put([]Run{run("1", judge("a", StatusSkip), judge("b", StatusError), judge("c", StatusInterrupted))}))
			got, _ := c.Lookup([]Key{key("a"), key("b"), key("c")})
			assert.Empty(t, got)
		})
	}
}
