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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
)

// markerKinds returns the real declarations that carry markers.
func markerKinds(t *testing.T) map[string]module.KindDecl {
	t.Helper()
	out := map[string]module.KindDecl{}
	for _, k := range filemod.New().Kinds() {
		for _, f := range k.Fields {
			if f.Name == filemod.FieldMarkers {
				out[k.Name] = k
			}
		}
	}
	require.Len(t, out, 2, "PreFileCreate and PreFileUpdate")
	return out
}

func TestMarkersDecl_TypoInsidePredicateIsRefused(t *testing.T) {
	// The bug this exists to prevent, written out: `.knid` for `.kind`, inside
	// the predicate. With a nil Elem this compiles, loads, and returns
	// admitted=false forever — a rule that looks satisfied.
	for kind, decl := range markerKinds(t) {
		t.Run(kind, func(t *testing.T) {
			_, err := guardrail.CompileMatcherFor(`any(markers, .knid == "docs")`, decl)
			require.Error(t, err, "a typo inside the predicate must be refused at load")
			assert.Contains(t, err.Error(), "knid", "the message names what was wrong")
		})
	}
}

func TestMarkersDecl_CorrectPredicateStillCompiles(t *testing.T) {
	// The other half. A refusal that refused everything would also pass the
	// test above, and would be a worse bug than the one it fixed.
	for kind, decl := range markerKinds(t) {
		t.Run(kind, func(t *testing.T) {
			for _, src := range []string{
				`any(markers, .kind == "docs")`,
				`any(markers, .fqn startsWith "pkg.")`,
				`any(markers, .line > 10)`,
				`all(markers, .kind != "banned")`,
				`none(markers, .fqn == "pkg.Thing")`,
				`one(markers, .kind == "blueprint")`,
				`len(markers) == 0`,
				`markers[0].kind == "blueprint"`,
				`any(markers, .kind == "docs" && .line < 100)`,
			} {
				_, err := guardrail.CompileMatcherFor(src, decl)
				assert.NoErrorf(t, err, "%q reads only declared fields", src)
			}
		})
	}
}

func TestMarkersDecl_EveryElementFieldNameIsCheckedAndNoOther(t *testing.T) {
	// Each declared name compiles; a near-miss of each is refused. A checker
	// that accepted everything, or that hard-coded one name, fails one half.
	for kind, decl := range markerKinds(t) {
		t.Run(kind, func(t *testing.T) {
			for good, bad := range map[string]string{
				"kind": "kinds",
				"fqn":  "fqns",
				"line": "lines",
			} {
				_, err := guardrail.CompileMatcherFor(`any(markers, .`+good+` != nil)`, decl)
				assert.NoErrorf(t, err, ".%s is declared", good)

				_, err = guardrail.CompileMatcherFor(`any(markers, .`+bad+` != nil)`, decl)
				assert.Errorf(t, err, ".%s is not declared and must be refused", bad)
			}
		})
	}
}

func TestMarkersDecl_LineIsAnIntegerNotAString(t *testing.T) {
	// TypeInt, not TypeString. Declared as a string, `.line > 10` — a correct
	// rule — would be refused; declared as nothing, `.line == "3"` — a
	// comparison that can never hold — would load.
	for kind, decl := range markerKinds(t) {
		t.Run(kind, func(t *testing.T) {
			_, err := guardrail.CompileMatcherFor(`any(markers, .line > 10)`, decl)
			require.NoError(t, err, "an integer comparison must be allowed")

			_, err = guardrail.CompileMatcherFor(`any(markers, .line == "3")`, decl)
			require.Error(t, err, "comparing a line against a string must be refused")
		})
	}
}

func TestMarkersDecl_RefusalNamesTheAvailableFields(t *testing.T) {
	// Validate's message is what an author reads. A refusal that did not say
	// what the fields ARE leaves them guessing at the spelling.
	decl := markerKinds(t)[filemod.KindPreCreate]
	_, err := guardrail.CompileMatcherFor(`any(markers, .knid == "docs")`, decl)
	require.Error(t, err)

	// The compile error names the offending name; Validate wraps it with the
	// kind's field list. Both halves are checked, since a caller reads the
	// wrapped form.
	reg, err := module.NewRegistryForTest(filemod.New())
	require.NoError(t, err)

	d := guardrail.Declaration{
		Name: "marker-rule",
		Dir:  t.TempDir(),
		Hooks: map[string][]guardrail.Binding{
			filemod.KindPreCreate: {{
				Matcher: `any(markers, .knid == "docs")`,
				Hooks:   []guardrail.Hook{{Type: guardrail.HookCommand, Command: "true"}},
			}},
		},
	}
	problems := guardrail.Validate(d, reg)
	require.NotEmpty(t, problems)

	var msg string
	for _, p := range problems {
		if errors.Is(p, guardrail.ErrBadMatcher) {
			msg = p.Message()
		}
	}
	require.NotEmpty(t, msg, "the fault must be reported as a bad matcher")
	assert.Contains(t, msg, "knid")
	for _, field := range []string{filemod.FieldPath, filemod.FieldContent, filemod.FieldMarkers} {
		assert.Containsf(t, msg, field, "the message should name %q as available", field)
	}
}

func TestMarkersDecl_DeleteHasNoMarkersToBindTo(t *testing.T) {
	// A rule that tried to read markers off a deletion must be refused, not
	// silently handed an empty list forever.
	var del module.KindDecl
	for _, k := range filemod.New().Kinds() {
		if k.Name == filemod.KindPreDelete {
			del = k
		}
	}
	require.Equal(t, filemod.KindPreDelete, del.Name)

	_, err := guardrail.CompileMatcherFor(`len(markers) == 0`, del)
	require.Error(t, err, "a deletion declares no markers, so reading them is an author error")
	assert.Contains(t, err.Error(), "markers")
}
