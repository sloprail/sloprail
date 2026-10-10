package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeHarness is a harness that is NOT Claude Code, for the tests about a
// concrete name being skipped under the wrong harness. Using a real second
// harness would mean waiting for one to be supported; using a fake means the
// "wrong harness" case is exercised today.
func fakeHarness(name Harness, offered ...string) harnessSpec {
	set := make(map[string]bool, len(offered))
	for _, model := range offered {
		set[model] = true
	}
	return harnessSpec{
		name:   name,
		binary: string(name),
		detect: func(getenv func(string) string) bool { return false },
		sizes: map[SizeAlias]string{
			SizeXS:  string(name) + "-xs",
			SizeSM:  string(name) + "-sm",
			SizeMD:  string(name) + "-md",
			SizeLG:  string(name) + "-lg",
			SizeXL:  string(name) + "-xl",
			SizeXXL: string(name) + "-xxl",
		},
		offers:   func(model string) bool { return set[model] },
		argsFlag: "--" + string(name) + "-args",
	}
}

func mustParse(t *testing.T, set string) []Preference {
	t.Helper()
	prefs, err := ParseModelSet(set)
	require.NoError(t, err)
	return prefs
}

// --- classification -------------------------------------------------------

// The whole design rests on classification being lexical and complete: all six
// aliases, and only those six.
func TestParseModelSet_ClassifiesEverySizeAlias(t *testing.T) {
	for _, alias := range []string{
		"size-xs", "size-sm", "size-md", "size-lg", "size-xl", "size-xxl",
	} {
		prefs := mustParse(t, alias)
		require.Len(t, prefs, 1)
		assert.Equal(t, KindAlias, prefs[0].Kind, "%s must classify as an alias", alias)
		assert.Equal(t, SizeAlias(alias), prefs[0].Alias)
	}
}

// The falsifier for "the prefix is what makes an entry readable on sight": a
// bare size name is NOT an alias. If it were, a harness shipping a model called
// `md` could not be named at all.
func TestParseModelSet_BareSizeNamesAreConcrete(t *testing.T) {
	for _, bare := range []string{"xs", "sm", "md", "lg", "xl", "xxl", "MD", "SIZE-MD", "Size-Md"} {
		prefs := mustParse(t, bare)
		require.Len(t, prefs, 1)
		assert.Equal(t, KindConcrete, prefs[0].Kind,
			"%q must be a concrete model name, not an alias — the size- prefix is what distinguishes them, and aliases are lower-case", bare)
	}
}

// A name merely sharing the prefix is concrete, not a malformed alias.
func TestParseModelSet_UnknownSizePrefixIsConcrete(t *testing.T) {
	prefs := mustParse(t, "size-huge")
	require.Len(t, prefs, 1)
	assert.Equal(t, KindConcrete, prefs[0].Kind)
	assert.Equal(t, "size-huge", prefs[0].Raw)
}

func TestParseModelSet_TrimsWhitespace(t *testing.T) {
	prefs := mustParse(t, " size-md , claude-opus-5 ")
	require.Len(t, prefs, 2)
	assert.Equal(t, KindAlias, prefs[0].Kind)
	assert.Equal(t, "claude-opus-5", prefs[1].Raw)
}

func TestParseModelSet_EmptyIsRefused(t *testing.T) {
	for _, set := range []string{"", "   "} {
		_, err := ParseModelSet(set)
		assert.ErrorIs(t, err, ErrEmptyModelSet)
	}
}

// A stray comma is a typo, not an entry to skip: the model the author meant to
// write there is missing.
func TestParseModelSet_StrayCommaIsRefused(t *testing.T) {
	for _, set := range []string{"size-md,", ",size-md", "size-md,,size-lg"} {
		_, err := ParseModelSet(set)
		assert.ErrorIs(t, err, ErrEmptyModelSet, "set %q", set)
	}
}

// --- an alias always resolves ---------------------------------------------

// The load-bearing property: under every registered harness, every alias
// resolves to a non-empty model. Not a sample — all of them, under all of them.
func TestResolveModelSet_EveryAliasResolvesUnderEveryHarness(t *testing.T) {
	require.NoError(t, aliasesComplete())

	for _, spec := range harnesses {
		for _, alias := range sizeAliases {
			res, err := ResolveModelSet(mustParse(t, string(alias)), spec)
			require.NoError(t, err, "%s under %s must resolve", alias, spec.name)
			assert.NotEmpty(t, res.Model)
			assert.Equal(t, KindAlias, res.Matched.Kind)
		}
	}
}

// An alias resolves even in a harness that offers no concrete model at all —
// which is what "harness-agnostic" means.
func TestResolveModelSet_AliasResolvesWithoutAnyConcreteModel(t *testing.T) {
	spec := fakeHarness("codex") // offers nothing
	res, err := ResolveModelSet(mustParse(t, "size-lg"), spec)
	require.NoError(t, err)
	assert.Equal(t, "codex-lg", res.Model)
}

// --- concrete models are harness-specific ---------------------------------

// The owner's worked example: preference order within one harness.
func TestResolveModelSet_PrefersEarlierConcreteModel(t *testing.T) {
	spec := fakeHarness("codex", "codex-a", "codex-b")
	res, err := ResolveModelSet(mustParse(t, "codex-a,codex-b"), spec)
	require.NoError(t, err)
	assert.Equal(t, "codex-a", res.Model)
	assert.Empty(t, res.Skipped)
}

// The same example when the first choice is not offered: fall to the second.
func TestResolveModelSet_FallsToNextWhenFirstNotOffered(t *testing.T) {
	spec := fakeHarness("codex", "codex-b")
	res, err := ResolveModelSet(mustParse(t, "codex-a,codex-b"), spec)
	require.NoError(t, err)
	assert.Equal(t, "codex-b", res.Model)
	require.Len(t, res.Skipped, 1)
	assert.Equal(t, "codex-a", res.Skipped[0].Raw)
}

// The owner's other worked example, exactly:
//
//	--model claude-3-7,claude-4,codex-whatever  under codex
//
// Both Claude entries are skipped because this harness does not offer them, and
// codex-whatever is chosen. The list means "best available here".
func TestResolveModelSet_OwnersWorkedExample_ClaudeEntriesSkippedUnderCodex(t *testing.T) {
	spec := fakeHarness("codex", "codex-whatever")
	res, err := ResolveModelSet(mustParse(t, "claude-3-7,claude-4,codex-whatever"), spec)
	require.NoError(t, err)

	assert.Equal(t, "codex-whatever", res.Model)
	assert.Equal(t, 2, res.MatchedIndex)
	assert.Equal(t, []string{"claude-3-7", "claude-4"}, rawNames(res.Skipped))
	assert.Empty(t, res.Unreachable)
}

// The falsifier for "a concrete name matches only under the harness that offers
// it": the SAME entry resolves under one harness and is skipped under another.
// A resolver that ignored the harness would pass one of these and fail the
// other, so asserting both is what makes the claim real.
func TestResolveModelSet_SameEntryMatchesOrSkipsByHarness(t *testing.T) {
	set := mustParse(t, "claude-opus-5,size-xxl")

	underClaude, err := ResolveModelSet(set, claudeCodeSpec)
	require.NoError(t, err)
	assert.Equal(t, "claude-opus-5", underClaude.Model,
		"Claude Code offers this name, so it must match before the alias is reached")

	underCodex, err := ResolveModelSet(set, fakeHarness("codex"))
	require.NoError(t, err)
	assert.Equal(t, "codex-xxl", underCodex.Model,
		"codex does not offer a claude- name, so it must be skipped and the alias reached")
	assert.Equal(t, []string{"claude-opus-5"}, rawNames(underCodex.Skipped))
}

// --- an alias makes later entries unreachable ------------------------------

// The owner's third worked example: `size-md,claude-sonnet-4-7`. size-md always
// resolves, so the second entry is unreachable — reported, not refused.
func TestResolveModelSet_AliasMakesLaterEntriesUnreachable(t *testing.T) {
	res, err := ResolveModelSet(mustParse(t, "size-md,claude-sonnet-4-7"), claudeCodeSpec)
	require.NoError(t, err, "an unreachable entry is worth saying out loud, not refusing")

	assert.Equal(t, claudeCodeSpec.sizes[SizeMD], res.Model)
	assert.Equal(t, 0, res.MatchedIndex)
	assert.Equal(t, []string{"claude-sonnet-4-7"}, rawNames(res.Unreachable))
}

// Everything after an alias is unreachable, however much of it there is, and
// even when a later entry is one this harness genuinely offers. That last part
// is the falsifier: an implementation that only reported unreachable entries it
// could not otherwise resolve would pass a weaker version of this test.
func TestResolveModelSet_EverythingAfterAnAliasIsUnreachable(t *testing.T) {
	res, err := ResolveModelSet(
		mustParse(t, "size-sm,claude-opus-5,size-xxl,claude-sonnet-4-5"), claudeCodeSpec)
	require.NoError(t, err)

	assert.Equal(t, claudeCodeSpec.sizes[SizeSM], res.Model)
	assert.Equal(t,
		[]string{"claude-opus-5", "size-xxl", "claude-sonnet-4-5"},
		rawNames(res.Unreachable),
		"an alias ends the search, so every later entry is unreachable — including ones this harness offers")
}

// The complement, and the falsifier for "unreachable means everything after the
// match": a CONCRETE match has entries after it too, but those were merely not
// preferred — under a harness that did not offer codex-a, the walk would reach
// codex-b. Warning about them would fire on an ordinary, correctly written set,
// and a warning that fires on correct input is one an author learns to ignore.
func TestResolveModelSet_ConcreteMatchMakesNothingUnreachable(t *testing.T) {
	res, err := ResolveModelSet(mustParse(t, "codex-a,codex-b"), fakeHarness("codex", "codex-a"))
	require.NoError(t, err)
	assert.Equal(t, "codex-a", res.Model)
	assert.Empty(t, res.Unreachable,
		"codex-b is reachable under a harness that does not offer codex-a, so it is not unreachable")

	// And it genuinely is reachable there, which is what makes the claim true.
	res, err = ResolveModelSet(mustParse(t, "codex-a,codex-b"), fakeHarness("codex", "codex-b"))
	require.NoError(t, err)
	assert.Equal(t, "codex-b", res.Model)
}

// --- a set resolving to nothing is a refusal -------------------------------

// The refusal rule: no fallback to a default, because a verdict from a model
// the author did not choose is one nobody can account for.
func TestResolveModelSet_NothingAvailableIsRefused(t *testing.T) {
	spec := fakeHarness("codex", "codex-a")
	_, err := ResolveModelSet(mustParse(t, "claude-opus-5,claude-sonnet-4-5"), spec)

	require.ErrorIs(t, err, ErrNoModelAvailable)
	// The refusal must name what was asked for and what is running, or the
	// author cannot act on it.
	assert.Contains(t, err.Error(), "claude-opus-5")
	assert.Contains(t, err.Error(), "codex")
}

// The falsifier for "refusal, not fallback": the harness DOES offer a model, it
// is simply not one the set named. An implementation that fell back to a
// default would find something to run here and return no error.
func TestResolveModelSet_DoesNotFallBackToAnAvailableModel(t *testing.T) {
	spec := fakeHarness("codex", "codex-a", "codex-b")
	res, err := ResolveModelSet(mustParse(t, "claude-opus-5"), spec)

	require.Error(t, err, "a set naming nothing this harness offers must refuse, even though codex-a exists")
	assert.ErrorIs(t, err, ErrNoModelAvailable)
	assert.Empty(t, res.Model, "no model may be chosen when the set matched nothing")
}

func TestResolveModelSet_EmptySetIsRefused(t *testing.T) {
	_, err := ResolveModelSet(nil, claudeCodeSpec)
	assert.ErrorIs(t, err, ErrEmptyModelSet)
}

// A harness registry with a hole must be caught, because a non-resolving alias
// would silently make the entries after it reachable — the exact breakage
// aliasesComplete exists to prevent.
func TestResolveModelSet_MissingAliasMappingIsRefusedNotSkipped(t *testing.T) {
	spec := fakeHarness("codex", "codex-a")
	delete(spec.sizes, SizeMD)

	_, err := ResolveModelSet(mustParse(t, "size-md,codex-a"), spec)
	require.Error(t, err, "a missing mapping must refuse, not fall through to codex-a")
	assert.Contains(t, err.Error(), "size-md")
}

func TestAliasesComplete_CatchesAHoleInTheRegistry(t *testing.T) {
	require.NoError(t, aliasesComplete(), "the real registry must be complete")

	// And the check itself must actually detect a hole — otherwise it is a
	// no-op that passes for the wrong reason.
	broken := fakeHarness("codex")
	delete(broken.sizes, SizeXXL)
	saved := harnesses
	harnesses = []harnessSpec{broken}
	t.Cleanup(func() { harnesses = saved })

	err := aliasesComplete()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "size-xxl")
}

// --- Claude Code's own catalogue ------------------------------------------

// Every alias must map to a name `claude --model` actually accepts. These four
// family aliases were each confirmed by running claude for real; this pins the
// table so a later edit cannot quietly introduce a name nobody verified.
func TestClaudeCodeSpec_AliasesMapToVerifiedFamilyAliases(t *testing.T) {
	verified := map[string]bool{"haiku": true, "sonnet": true, "opus": true, "fable": true}
	for _, alias := range sizeAliases {
		model := claudeCodeSpec.sizes[alias]
		assert.True(t, verified[model],
			"%s maps to %q, which is not one of the family aliases confirmed to run", alias, model)
	}
}

// Sizes must not invert: a larger alias may map to the same family as a smaller
// one, but never to a smaller one.
func TestClaudeCodeSpec_SizesDoNotInvert(t *testing.T) {
	rank := map[string]int{"haiku": 0, "sonnet": 1, "opus": 2, "fable": 3}
	for i := 1; i < len(sizeAliases); i++ {
		prev := claudeCodeSpec.sizes[sizeAliases[i-1]]
		cur := claudeCodeSpec.sizes[sizeAliases[i]]
		assert.GreaterOrEqual(t, rank[cur], rank[prev],
			"%s (%s) must not be smaller than %s (%s)", sizeAliases[i], cur, sizeAliases[i-1], prev)
	}
}

func TestClaudeCodeSpec_OffersItsOwnNames(t *testing.T) {
	for _, model := range []string{
		"claude-opus-5", "claude-sonnet-4-5", "claude-haiku-4-5",
		"haiku", "sonnet", "opus", "fable",
	} {
		assert.True(t, claudeCodeSpec.offers(model), "%s is a Claude Code model name", model)
	}
}

func TestClaudeCodeSpec_DoesNotOfferAnotherHarnessNames(t *testing.T) {
	for _, model := range []string{"gpt-5", "codex-whatever", "gemini-2", "sonnet-4-thinking"} {
		assert.False(t, claudeCodeSpec.offers(model),
			"%s is not a Claude Code name and must be skipped under Claude Code", model)
	}
}

// A name Claude Code does not actually have is still treated as ABOUT Claude
// Code, so it is refused by the harness rather than silently replaced by some
// other model. Verified for real: `claude --model claude-sonnet-4-7` exits 1.
func TestClaudeCodeSpec_UnknownClaudeNameIsMatchedNotSkipped(t *testing.T) {
	res, err := ResolveModelSet(mustParse(t, "claude-sonnet-4-7,size-md"), claudeCodeSpec)
	require.NoError(t, err)
	assert.Equal(t, "claude-sonnet-4-7", res.Model,
		"a claude- name must reach the harness, which is the authority on whether it exists; "+
			"skipping it would silently run the alias instead")
}

func TestRawNames_PreservesOrderAndText(t *testing.T) {
	prefs := mustParse(t, "size-md, claude-opus-5")
	assert.Equal(t, []string{"size-md", "claude-opus-5"}, rawNames(prefs))
}

// The refusal must be actionable: it tells the author aliases exist.
func TestResolveModelSet_RefusalSuggestsAliases(t *testing.T) {
	_, err := ResolveModelSet(mustParse(t, "gpt-5"), claudeCodeSpec)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "size-md"),
		"the refusal should point at aliases as the harness-agnostic way out")
}

// The two Opus sizes are told apart by reasoning effort, and only a size carries one:
// a concrete model name runs at the harness's own default.
func TestResolveModelSet_ClaudeSizesCarryTheirEffort(t *testing.T) {
	for set, want := range map[string][]string{
		"size-md":         nil,
		"size-lg":         {"--effort", "medium"},
		"size-xl":         {"--effort", "high"},
		"claude-opus-5-5": nil,
	} {
		res, err := ResolveModelSet(mustParse(t, set), claudeCodeSpec)
		if err != nil {
			t.Fatalf("%s: %v", set, err)
		}
		if !slices.Equal(res.Args, want) {
			t.Errorf("%s: args = %v, want %v", set, res.Args, want)
		}
	}
	lg, _ := ResolveModelSet(mustParse(t, "size-lg"), claudeCodeSpec)
	xl, _ := ResolveModelSet(mustParse(t, "size-xl"), claudeCodeSpec)
	if lg.Model != xl.Model {
		t.Errorf("size-lg and size-xl are the same model at two efforts, got %q and %q", lg.Model, xl.Model)
	}
}
