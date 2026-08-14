package commandmod_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
)

// The element shapes inside an invocation.
//
// module.FieldDecl.Elem exists so that a name inside a predicate is checked the
// same way a top-level one is: without it "the collection is checked and the
// predicate body is not", and a typo one level down loads, never fires, and
// reads as a rule being satisfied.
//
// `invocations` declares its Elem. Two of that element's own fields did not:
//
//	argv   declared TypeList with a nil Elem, so `any(.argv, ...)` had its body
//	       checked against types.Any — every name inside it accepted.
//	flags  declared TypeMap with no Fields, which is CORRECT and stays that way:
//	       a flag name is the command author's, not this module's, so there is no
//	       vocabulary to enumerate and a closed type would refuse `--access` for
//	       the engine's missing knowledge rather than for the author's mistake.
//
// The distinction is the one FieldDecl already draws. A list's element shape CAN
// be enumerated — every entry of argv is a string, always — so a module that
// knows it should declare it and get the check. A map's keys cannot.

func kindDecl(t *testing.T, name string) module.KindDecl {
	t.Helper()
	for _, k := range commandmod.New().Kinds() {
		if k.Name == name {
			return k
		}
	}
	t.Fatalf("commandmod declares no kind %q", name)
	return module.KindDecl{}
}

// TestArgvDeclaresItsElement.
//
// `argv` holds strings. Declaring that is what makes a predicate over it
// checkable, and the check is the whole reason Elem exists.
func TestArgvDeclaresItsElement(t *testing.T) {
	k := kindDecl(t, commandmod.KindPreInvoke)

	var invocations *module.FieldDecl
	for i, f := range k.Fields {
		if f.Name == commandmod.FieldInvocations {
			invocations = &k.Fields[i]
		}
	}
	require.NotNil(t, invocations, "the kind must declare %s", commandmod.FieldInvocations)
	require.NotNil(t, invocations.Elem, "an invocation's own shape must be declared")

	var argv *module.FieldDecl
	for i, f := range invocations.Elem.Fields {
		if f.Name == commandmod.KeyArgv {
			argv = &invocations.Elem.Fields[i]
		}
	}
	require.NotNil(t, argv, "an invocation must declare %s", commandmod.KeyArgv)
	require.Equal(t, module.TypeList, argv.Type)

	require.NotNil(t, argv.Elem,
		"argv is a list of strings and the module knows it; leaving Elem nil checks the "+
			"collection and not the predicate body, which is the silent never-fires one level down")
	assert.Equal(t, module.TypeString, argv.Elem.Type)
}

// TestArgvPredicateTypoIsRefusedAtLoad.
//
// The failure the declaration buys back. `#.bin` inside an argv predicate is a
// rule author reaching for the invocation's field from inside the wrong scope —
// argv's elements are strings and have no fields at all — and with argv's Elem
// nil it compiled, loaded, and returned false on every command line forever.
//
// A rule written to catch `rm -rf` that never fires is worse than one that will
// not load, because the first looks like a rule being satisfied.
func TestArgvPredicateTypoIsRefusedAtLoad(t *testing.T) {
	k := kindDecl(t, commandmod.KindPreInvoke)

	_, err := guardrail.CompileMatcherFor(`any(invocations, any(.argv, #.bin == "rm"))`, k)
	assert.Error(t, err,
		"a field read off an argv element must be refused at load: argv holds strings")
}

// TestArgvStringPredicatesStillLoad.
//
// The other half, and the one a too-strict declaration would break. Declaring
// the element as a string must not refuse the ordinary spellings a rule about
// arguments is written in.
func TestArgvStringPredicatesStillLoad(t *testing.T) {
	k := kindDecl(t, commandmod.KindPreInvoke)

	for _, src := range []string{
		`any(invocations, any(.argv, # == "-rf"))`,
		`any(invocations, any(.argv, # startsWith "--"))`,
		`any(invocations, any(.argv, # endsWith ".md"))`,
		`any(invocations, "-rf" in .argv)`,
		`any(invocations, len(.argv) > 2)`,
		`any(invocations, .argv[0] == "rm")`,
	} {
		_, err := guardrail.CompileMatcherFor(src, k)
		assert.NoErrorf(t, err, "a rule about arguments must still load: %s", src)
	}
}

// TestFlagsStayOpen.
//
// Deliberately NOT closed, and this pins it against a well-meant "declare
// everything" change. A flag name belongs to the command being run, so
// enumerating them here would refuse `--access` because this engine has not
// heard of npm — the checker punishing an author for our missing vocabulary
// rather than for their mistake. fieldType leaves such a map at types.Any for
// exactly this reason.
func TestFlagsStayOpen(t *testing.T) {
	k := kindDecl(t, commandmod.KindPreInvoke)

	for _, src := range []string{
		`any(invocations, .flags.access == "public")`,
		`any(invocations, "force" in .flags)`,
		`any(invocations, .flags.anythingAtAll == "")`,
	} {
		_, err := guardrail.CompileMatcherFor(src, k)
		assert.NoErrorf(t, err, "a flag name is the command's, not this module's: %s", src)
	}
}
