package main

import (
	"errors"
	"fmt"
	"strings"
)

// ErrEmptyModelSet is returned when `--model` was given nothing to resolve.
var ErrEmptyModelSet = errors.New("empty model set")

// ErrNoModelAvailable is returned when no entry in a model set matches the
// running harness.
var ErrNoModelAvailable = errors.New("no model in the set is available")

// PreferenceKind distinguishes the two things a model-set entry can be.
type PreferenceKind int

const (
	// KindAlias is a size — `size-md`. Harness-agnostic; always resolves.
	KindAlias PreferenceKind = iota

	// KindConcrete is one harness's own model name — `claude-sonnet-4-5`.
	// Resolves only under the harness that offers it.
	KindConcrete
)

// Preference is one parsed entry of a model set.
type Preference struct {
	// Kind says which of the two this is.
	Kind PreferenceKind

	// Raw is the entry exactly as written, for diagnostics. A refusal that
	// echoes back what the author typed is one they can act on.
	Raw string

	// Alias is the size, set only when Kind is KindAlias.
	Alias SizeAlias
}

// Resolution is the outcome of resolving a model set.
type Resolution struct {
	// Model is the harness-native model name to run.
	Model string

	// Matched is the entry that produced it.
	Matched Preference

	// MatchedIndex is that entry's position in the set.
	MatchedIndex int

	// Skipped is every entry before the match, all of them concrete names this
	// harness does not offer. Reported so an author whose first choice was
	// skipped learns it happened rather than wondering why a later model ran.
	Skipped []Preference

	// Unreachable is every entry after the match when an ALIAS matched — the
	// entries that can never be reached under ANY harness, because an alias
	// resolves everywhere and so always ends the search. Worth saying out loud:
	// an author who wrote `size-md,claude-opus-5` probably meant the other
	// order.
	//
	// Empty when a CONCRETE entry matched, even though entries follow it there
	// too. Those were merely not preferred: had this harness not offered the
	// match, the walk would have continued into them, and under another harness
	// it still will. Calling them unreachable would be a false claim about an
	// ordinary, correctly written set — and a warning that fires on correct
	// input is one an author learns to ignore.
	Unreachable []Preference
}

// ParseModelSet splits a comma-separated model set into entries and classifies
// each one.
//
// Classification is purely lexical — an entry is an alias if it is one of the
// six size names, and a concrete model otherwise. That is exactly what the
// `size-` prefix buys: no catalogue is consulted to decide what KIND an entry
// is, so the same string classifies identically under every harness, and a
// reader can tell by looking.
//
// Surrounding whitespace is trimmed, because a set is often written by hand and
// `size-md, claude-opus-5` should mean what it looks like it means. An empty
// entry — a stray or trailing comma — is an error rather than something skipped
// silently: it is a typo, and the entry the author meant to write there is
// missing.
func ParseModelSet(set string) ([]Preference, error) {
	if strings.TrimSpace(set) == "" {
		return nil, fmt.Errorf(
			"%w: --model needs at least one entry — a size alias (%s) or a model name",
			ErrEmptyModelSet, strings.Join(aliasNames(), ", "))
	}

	parts := strings.Split(set, ",")
	prefs := make([]Preference, 0, len(parts))
	for i, part := range parts {
		entry := strings.TrimSpace(part)
		if entry == "" {
			return nil, fmt.Errorf(
				"%w: entry %d of --model is empty (a stray or trailing comma in %q)",
				ErrEmptyModelSet, i+1, set)
		}
		prefs = append(prefs, classify(entry))
	}
	return prefs, nil
}

// classify decides whether one entry is a size alias or a concrete model name.
//
// Only the six exact size names are aliases. A name that merely starts with
// `size-` — `size-huge` — is NOT an alias: it is a concrete model name that
// happens to share the prefix, and it is resolved as one. Treating it as a
// malformed alias would be guessing at what the author meant, and the harness
// is better placed to say whether it has a model by that name.
func classify(entry string) Preference {
	for _, alias := range sizeAliases {
		if entry == string(alias) {
			return Preference{Kind: KindAlias, Raw: entry, Alias: alias}
		}
	}
	return Preference{Kind: KindConcrete, Raw: entry}
}

// aliasNames is the six sizes as strings, for diagnostics.
func aliasNames() []string {
	names := make([]string, 0, len(sizeAliases))
	for _, alias := range sizeAliases {
		names = append(names, string(alias))
	}
	return names
}

// ResolveModelSet walks a model set in order and returns the first entry the
// running harness can satisfy.
//
// The two kinds are resolved differently, and that difference is the design:
//
//   - An ALIAS resolves through this harness's size table, which is complete by
//     construction (aliasesComplete pins it). So an alias always matches, and
//     the walk ends there — everything after it is unreachable, reported but
//     not refused.
//
//   - A CONCRETE name matches only when this harness offers it. Under another
//     harness it is skipped and the walk continues, which is what lets one set
//     name several harnesses' models and mean "the best available here" rather
//     than "this exact model".
//
// A set that matches nothing is REFUSED. Falling back to a default would
// produce a verdict reached by a model the author did not choose and cannot
// account for, which for a judging guardrail is worse than not running.
func ResolveModelSet(prefs []Preference, spec harnessSpec) (Resolution, error) {
	if len(prefs) == 0 {
		return Resolution{}, fmt.Errorf(
			"%w: --model needs at least one entry", ErrEmptyModelSet)
	}

	for i, pref := range prefs {
		var model string
		switch pref.Kind {
		case KindAlias:
			model = spec.sizes[pref.Alias]
			// Empty would mean the registry has a hole aliasesComplete should
			// have caught. Skipping would silently make the entries after this
			// one reachable — the exact breakage that check exists to prevent —
			// so it is a refusal naming the harness and the alias instead.
			if model == "" {
				return Resolution{}, fmt.Errorf(
					"harness %s maps no model for %s", spec.name, pref.Alias)
			}
		case KindConcrete:
			if !spec.offers(pref.Raw) {
				continue
			}
			model = pref.Raw
		}

		// Only an alias makes what follows unreachable — see Resolution.
		var unreachable []Preference
		if pref.Kind == KindAlias {
			unreachable = append([]Preference(nil), prefs[i+1:]...)
		}

		return Resolution{
			Model:        model,
			Matched:      pref,
			MatchedIndex: i,
			Skipped:      append([]Preference(nil), prefs[:i]...),
			Unreachable:  unreachable,
		}, nil
	}

	return Resolution{}, fmt.Errorf(
		"%w under harness %s: none of %s is a model it offers. "+
			"Add a size alias (%s) to name a size instead of a model — an alias resolves under every harness",
		ErrNoModelAvailable, spec.name,
		strings.Join(rawNames(prefs), ", "),
		strings.Join(aliasNames(), ", "))
}

// rawNames is a set's entries as written, for diagnostics.
func rawNames(prefs []Preference) []string {
	names := make([]string, 0, len(prefs))
	for _, pref := range prefs {
		names = append(names, pref.Raw)
	}
	return names
}
