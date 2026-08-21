// Package filemod_test holds the checks that need the guardrail package, which
// the filemod package itself must not import — the engine works from
// declarations rather than from anything compiled into it, and filemod reaching
// for the checker would invert that.
//
// What is proved here is the one thing a unit test inside filemod cannot: that
// the markers declaration actually causes the checker to REFUSE a typo. A test
// asserting the shape of the FieldDecl proves the shape; only running the
// checker proves the consequence, and the consequence is the entire reason the
// Elem field exists.
package filemod_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
)

// markerField pairs a kind's declaration with one of the marker fields it
// declares. Both oldMarkers and newMarkers share the same closed element shape,
// so the checks below run against every (kind, field) pair — a typo inside a
// predicate over either must be refused identically.
type markerField struct {
	kind  string
	field string
	decl  module.KindDecl
}

// markerKinds returns every declaration/field pair that carries a markers list,
// keyed by a "<kind>/<field>" label so a failing subtest names both.
func markerKinds(t *testing.T) map[string]markerField {
	t.Helper()
	out := map[string]markerField{}
	for _, k := range filemod.New().Kinds() {
		for _, f := range k.Fields {
			if f.Name == filemod.FieldOldMarkers || f.Name == filemod.FieldNewMarkers {
				out[k.Name+"/"+f.Name] = markerField{kind: k.Name, field: f.Name, decl: k}
			}
		}
	}
	// oldMarkers on PreUpdate, PreDelete, PostUpdate, PostDelete; newMarkers on
	// PreCreate, PreUpdate, PostCreate, PostUpdate — eight pairs in all.
	require.Len(t, out, 8, "every kind with prior text or a result carries a markers list")
	return out
}

func TestMarkersDecl_TypoInsidePredicateIsRefused(t *testing.T) {
	// The bug this exists to prevent, written out: `.knid` for `.kind`, inside
	// the predicate. With a nil Elem this compiles, loads, and returns
	// admitted=false forever — a rule that looks satisfied.
	for label, mf := range markerKinds(t) {
		t.Run(label, func(t *testing.T) {
			_, err := guardrail.CompileMatcherFor(
				fmt.Sprintf(`any(%s, .knid == "docs")`, mf.field), mf.decl)
			require.Error(t, err, "a typo inside the predicate must be refused at load")
			assert.Contains(t, err.Error(), "knid", "the message names what was wrong")
		})
	}
}

func TestMarkersDecl_CorrectPredicateStillCompiles(t *testing.T) {
	// The other half. A refusal that refused everything would also pass the
	// test above, and would be a worse bug than the one it fixed.
	for label, mf := range markerKinds(t) {
		t.Run(label, func(t *testing.T) {
			for _, form := range []string{
				`any(%[1]s, .kind == "docs")`,
				`any(%[1]s, .fqn startsWith "pkg.")`,
				`any(%[1]s, .line > 10)`,
				`all(%[1]s, .kind != "banned")`,
				`none(%[1]s, .fqn == "pkg.Thing")`,
				`one(%[1]s, .kind == "blueprint")`,
				`len(%[1]s) == 0`,
				`%[1]s[0].kind == "blueprint"`,
				`any(%[1]s, .kind == "docs" && .line < 100)`,
			} {
				src := fmt.Sprintf(form, mf.field)
				_, err := guardrail.CompileMatcherFor(src, mf.decl)
				assert.NoErrorf(t, err, "%q reads only declared fields", src)
			}
		})
	}
}

func TestMarkersDecl_EveryElementFieldNameIsCheckedAndNoOther(t *testing.T) {
	// Each declared name compiles; a near-miss of each is refused. A checker
	// that accepted everything, or that hard-coded one name, fails one half.
	for label, mf := range markerKinds(t) {
		t.Run(label, func(t *testing.T) {
			for good, bad := range map[string]string{
				"kind": "kinds",
				"fqn":  "fqns",
				"line": "lines",
			} {
				_, err := guardrail.CompileMatcherFor(
					fmt.Sprintf(`any(%s, .%s != nil)`, mf.field, good), mf.decl)
				assert.NoErrorf(t, err, ".%s is declared", good)

				_, err = guardrail.CompileMatcherFor(
					fmt.Sprintf(`any(%s, .%s != nil)`, mf.field, bad), mf.decl)
				assert.Errorf(t, err, ".%s is not declared and must be refused", bad)
			}
		})
	}
}

func TestMarkersDecl_LineIsAnIntegerNotAString(t *testing.T) {
	// TypeInt, not TypeString. Declared as a string, `.line > 10` — a correct
	// rule — would be refused; declared as nothing, `.line == "3"` — a
	// comparison that can never hold — would load.
	for label, mf := range markerKinds(t) {
		t.Run(label, func(t *testing.T) {
			_, err := guardrail.CompileMatcherFor(
				fmt.Sprintf(`any(%s, .line > 10)`, mf.field), mf.decl)
			require.NoError(t, err, "an integer comparison must be allowed")

			_, err = guardrail.CompileMatcherFor(
				fmt.Sprintf(`any(%s, .line == "3")`, mf.field), mf.decl)
			require.Error(t, err, "comparing a line against a string must be refused")
		})
	}
}

func TestMarkersDecl_RefusalNamesTheOffendingField(t *testing.T) {
	// The compile error an author reads must name the offending name, or they are
	// left guessing at the spelling.
	//
	// PreFileCreate declares newMarkers, so the predicate reads it. A typo inside
	// the predicate (`.knid` for `.kind`) fails to compile against the closed
	// element shape, and the error names `knid`.
	//
	// This checks the shared matcher machinery (guardrail.CompileMatcherFor). The
	// COMPLEMENTARY half — that a rule's validator wraps this compile error with
	// the fields the scope DOES carry so the author sees the available spelling —
	// now lives with the new-format validator that produces it:
	// internal/declaration TestLoad_FileGuard_SingularMarkerRefused asserts a
	// file-guard's bad-match refusal names path/markers/context.
	decl := markerKinds(t)[filemod.KindPreCreate+"/"+filemod.FieldNewMarkers]
	_, err := guardrail.CompileMatcherFor(`any(newMarkers, .knid == "docs")`, decl.decl)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "knid", "the compile error must name the offending field")
}

func TestMarkersDecl_DeleteHasNoNewMarkersToBindTo(t *testing.T) {
	// A rule that tried to read newMarkers off a deletion must be refused, not
	// silently handed an empty list forever. A delete declares oldMarkers — the
	// bytes about to be lost — but no newMarkers, because nothing remains to scan
	// for a result.
	var del module.KindDecl
	for _, k := range filemod.New().Kinds() {
		if k.Name == filemod.KindPreDelete {
			del = k
		}
	}
	require.Equal(t, filemod.KindPreDelete, del.Name)

	_, err := guardrail.CompileMatcherFor(`len(newMarkers) == 0`, del)
	require.Error(t, err, "a deletion declares no newMarkers, so reading them is an author error")
	assert.Contains(t, err.Error(), "newMarkers")

	// oldMarkers, on the other hand, is exactly what a delete carries.
	_, err = guardrail.CompileMatcherFor(`len(oldMarkers) == 0`, del)
	require.NoError(t, err, "a deletion declares oldMarkers — the annotations of the bytes about to be lost")
}
