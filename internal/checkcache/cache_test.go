package checkcache

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func key(fp string) Key {
	return Key{Rule: "file-guard/x", Kind: "check[0]:judge:./r.md.j2", Subject: "changeset", Fingerprint: fp}
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
		"rule":        {Rule: "file-guard/y", Kind: base.Kind, Subject: base.Subject, Fingerprint: "f"},
		"kind":        {Rule: base.Rule, Kind: "check[1]", Subject: base.Subject, Fingerprint: "f"},
		"subject":     {Rule: base.Rule, Kind: base.Kind, Subject: "a.go", Fingerprint: "f"},
		"fingerprint": key("g"),
	} {
		assert.NotEqual(t, base.ID(), k.ID(), name)
	}
	// Parts cannot be re-cut into one another.
	assert.NotEqual(t, Key{Rule: "ab", Kind: "c"}.ID(), Key{Rule: "a", Kind: "bc"}.ID())
}

// A verdict is a fact about its input: the rule's definition is not in the key, so editing the
// rule's yaml, script or template (a different RuleHash on the run) reads the same verdict.
func TestKey_TheRulesDefinitionIsNotPartOfTheIdentity(t *testing.T) {
	c := judge("f", "pass")
	old, edited := run("1", c), run("2", c)
	old.RuleHash, edited.RuleHash = "before-the-edit", "after-the-edit"
	assert.Equal(t, old.CheckKey(c), edited.CheckKey(c))
	assert.Equal(t, old.CheckKey(c).ID(), edited.CheckKey(c).ID())

	for _, impl := range implementations(t) {
		require.NoError(t, impl.Put([]Run{old}))
		got, err := impl.Lookup([]Key{edited.CheckKey(c)})
		require.NoError(t, err)
		assert.Len(t, got, 1, "the verdict reached under the old rule is found under the edited one")
		changed := judge("g", "pass")
		got, err = impl.Lookup([]Key{edited.CheckKey(changed)})
		require.NoError(t, err)
		assert.Empty(t, got, "a changed input is a different key")
	}
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

// Runs lists every run, the ones with nothing findable too (an engine failure, an empty
// range): that history is what a reader of what a rule passed or refused works over.
func TestCache_RunsListsEveryRunNewestFirstEachOnce(t *testing.T) {
	impls := implementations(t)
	impls["git"] = newRepo(t, "")
	for name, c := range impls {
		t.Run(name, func(t *testing.T) {
			empty := run("1")
			empty.HeadRef = "h-empty"
			failed := run("3")
			failed.ExitCode, failed.Error = 1, "git: bad"
			require.NoError(t, c.Put([]Run{empty, run("2", judge("a", StatusPass)), failed}))
			require.NoError(t, c.Put([]Run{run("2", judge("a", StatusPass))})) // the same run again

			got, err := c.Runs()
			require.NoError(t, err)
			var ids []string
			for _, r := range got {
				ids = append(ids, r.ID)
			}
			assert.Equal(t, []string{"run_3", "run_2", "run_1"}, ids)
			assert.Equal(t, "git: bad", got[0].Error)
		})
	}
}
