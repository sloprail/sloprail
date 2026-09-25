package declaration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A file-guard's `deletions:` key: absent means skip, each of the three values
// loads as written, and anything else is refused at load with a diagnostic that
// names the field, the bad value and the admitted ones — never read as the
// default, which would quietly switch off the deletions the author meant to catch.

func TestLoad_FileGuard_DeletionsAbsentIsSkip(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/g/file-guard.yaml": `
match: "**/*.md"
checks:
  - script: ./s.sh
`,
	})
	require.Len(t, loaded.FileGuards, 1)
	g := loaded.FileGuards[0]
	assert.Equal(t, Deletions(""), g.Deletions, "an absent key is stored as written — empty")
	assert.Equal(t, DeletionsSkip, g.Deletions.Mode(), "an absent key means skip")
}

func TestLoad_FileGuard_DeletionsEachValueLoads(t *testing.T) {
	for _, v := range []Deletions{DeletionsSkip, DeletionsInclude, DeletionsOnly} {
		t.Run(string(v), func(t *testing.T) {
			loaded := loadOK(t, map[string]string{
				"file-guard/g/file-guard.yaml": `
match: "**/*.md"
deletions: ` + string(v) + `
checks:
  - script: ./s.sh
`,
			})
			require.Len(t, loaded.FileGuards, 1)
			assert.Equal(t, v, loaded.FileGuards[0].Deletions)
			assert.Equal(t, v, loaded.FileGuards[0].Deletions.Mode())
		})
	}
}

func TestLoad_FileGuard_DeletionsUnknownValueRefused(t *testing.T) {
	for _, bad := range []string{"inlcude", "Include", "ONLY", "true", "all", "skip "} {
		t.Run(bad, func(t *testing.T) {
			iv := loadOneInvalid(t, map[string]string{
				"file-guard/g/file-guard.yaml": `
match: "**/*.md"
deletions: '` + bad + `'
checks:
  - script: ./s.sh
`,
			})
			assert.True(t, hasKind(iv, ErrBadValue), "an unknown deletions value is refused: %v", iv.Reason)
			assert.Contains(t, iv.Reason, "deletions", "the diagnostic names the field")
			assert.Contains(t, iv.Reason, bad, "the diagnostic quotes the bad value")
			assert.Contains(t, iv.Reason, "skip, include, only", "the diagnostic lists the admitted values")
		})
	}
}

// A YAML boolean is not a deletions value either: `deletions: true` reads as the
// string "true" and is refused, not taken as "include".
func TestLoad_FileGuard_DeletionsBooleanRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/g/file-guard.yaml": `
match: "**/*.md"
deletions: true
checks:
  - script: ./s.sh
`,
	})
	assert.True(t, hasKind(iv, ErrBadValue), "deletions: true is refused: %v", iv.Reason)
}

// Covers is the one filter every file-guard dispatch applies. The table is the
// whole contract: for each value, which of the six file kinds reach the guard.
func TestFileGuard_Covers(t *testing.T) {
	kinds := []string{
		KindPreFileCreate, KindPreFileUpdate, KindPreFileDelete,
		KindPostFileCreate, KindPostFileUpdate, KindPostFileDelete,
	}
	want := map[Deletions]map[string]bool{
		"": { // absent — the default, skip
			KindPreFileCreate: true, KindPreFileUpdate: true, KindPreFileDelete: false,
			KindPostFileCreate: true, KindPostFileUpdate: true, KindPostFileDelete: false,
		},
		DeletionsSkip: {
			KindPreFileCreate: true, KindPreFileUpdate: true, KindPreFileDelete: false,
			KindPostFileCreate: true, KindPostFileUpdate: true, KindPostFileDelete: false,
		},
		DeletionsInclude: {
			KindPreFileCreate: true, KindPreFileUpdate: true, KindPreFileDelete: true,
			KindPostFileCreate: true, KindPostFileUpdate: true, KindPostFileDelete: true,
		},
		DeletionsOnly: {
			KindPreFileCreate: false, KindPreFileUpdate: false, KindPreFileDelete: true,
			KindPostFileCreate: false, KindPostFileUpdate: false, KindPostFileDelete: true,
		},
	}
	for mode, byKind := range want {
		g := FileGuard{Name: "g", Deletions: mode}
		for _, k := range kinds {
			assert.Equal(t, byKind[k], g.Covers(k), "deletions=%q on %s", mode, k)
		}
		// A kind that is not a file event is not this filter's to refuse.
		assert.True(t, g.Covers(KindStop), "deletions=%q passes a non-file kind through", mode)
	}
}

func TestIsFileDeleteKind(t *testing.T) {
	assert.True(t, IsFileDeleteKind(KindPreFileDelete))
	assert.True(t, IsFileDeleteKind(KindPostFileDelete))
	for _, k := range []string{KindPreFileCreate, KindPreFileUpdate, KindPostFileCreate, KindPostFileUpdate, KindStop, ""} {
		assert.False(t, IsFileDeleteKind(k), k)
	}
}
