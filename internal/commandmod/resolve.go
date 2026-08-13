package commandmod

import (
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// expandPerWord expands each word on its own, keeping those that resolve and
// dropping those that do not.
//
// This is the honest floor of static resolution. A word that cannot be
// resolved without running something is not guessed at and not replaced with a
// placeholder — a placeholder in argv[0] would be a program name no rule
// should match, and one further along would be a value the command never
// receives. It is simply absent, and the vector is what remains.
func expandPerWord(cfg *expand.Config, words []*syntax.Word) []field {
	var fields []field
	for _, w := range words {
		lit := isLiteral(w)
		got, err := expand.Fields(cfg, w)
		if err != nil {
			// This word cannot be resolved without running something. It is
			// recorded as having been there and lost, because whether the
			// program word survived is what decides if the rest can be
			// trusted.
			fields = append(fields, field{lost: true})
			continue
		}
		if len(got) == 0 {
			// The word expanded to nothing at all — an unset parameter, the
			// commonest case. It leaves no field behind, so without recording
			// it here the next word would silently slide into first place and
			// be reported as the program. That is the shift the gotcha warns
			// about, and it is invisible unless the vanishing is written down.
			fields = append(fields, field{lost: true})
			continue
		}
		for _, g := range got {
			fields = append(fields, field{value: g, literal: lit})
		}
	}
	return fields
}

// field is one expanded word plus what is known about where it came from.
//
// The provenance is the whole point. expand.Fields returns a flat []string, so
// `$UNSET npm publish` and `$NPM publish` both arrive as a one-element vector
// holding a real word — `npm` in the first, `publish` in the second. They
// parse identically (a parameter that expands to nothing, then literals), so
// nothing in the flattened result can tell an environment prefix that vanished
// from a program that vanished. Keeping each field's origin is what makes the
// difference visible, and the answer in both cases is to emit nothing rather
// than promote the survivor and invent a program.
type field struct {
	value string
	// literal is true when the source word had no expansion in it at all — no
	// parameter, no substitution, no glob — so its value is exactly what was
	// typed and is certain. This is the only property the program word is
	// judged on: a non-literal word may resolve to a real-looking name and
	// still be a guess about the environment.
	literal bool
	// lost marks a word that could not be resolved, or that resolved to
	// nothing. It carries no value and never reaches argv. It exists so a
	// vanished word still occupies a position — without it the next word
	// silently slides into first place, which is the vector shift the gotcha
	// warns about.
	lost bool
}

// isLiteral reports whether a word is certain — every part a plain literal,
// nothing that expansion could turn into something else or into nothing.
//
// A quoted literal counts: `"npm"` and `n""pm` are certain, because quote
// removal is not resolution against anything unknown. A parameter, a command
// or process substitution, or an arithmetic expression does not.
func isLiteral(w *syntax.Word) bool {
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
		case *syntax.SglQuoted:
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				if _, ok := inner.(*syntax.Lit); !ok {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

// resolve turns one parsed call into the invocations it performs.
//
// Assigns are not consulted: mvdan/sh already separates an environment prefix
// from the argument vector, so `FOO=1 npm publish` arrives with Args holding
// npm and publish alone. That separation is the whole reason for parsing
// rather than matching the string — a regex cannot tell an assignment from a
// program.
func resolve(cfg *expand.Config, call *syntax.CallExpr) []Invocation {
	if len(call.Args) == 0 {
		// A bare assignment — `FOO=1` on its own — parses as a call with no
		// arguments. It runs no program, so it is not an invocation.
		return nil
	}

	// Always word by word, never as one vector.
	//
	// Expanding the vector whole is what loses the program's provenance: a
	// flat []string cannot say whether its first element came from the first
	// word or from the second after the first expanded away. It also fails
	// the whole vector when any single word is unresolvable, which would drop
	// `echo` from `echo $(npm publish)` even though echo is genuinely about
	// to run. Per-word gives both: what survives is kept, and each survivor
	// still knows where it came from.
	fields := expandPerWord(cfg, call.Args)
	if len(fields) == 0 {
		return nil
	}

	// The program is believable only if the first field came from a literal
	// word. That single test covers every way the program can be unknowable:
	//
	//   $NPM publish          word 0 vanished, so field 0 is `publish` from
	//                         word 1 — an argument sliding into the program's
	//                         place. Non-literal words are the only ones that
	//                         can vanish, so the shift cannot happen unless
	//                         some earlier word was non-literal.
	//   $(which npm) publish  the same shift, by a substitution that errored.
	//   "$EDITOR" file.txt    from word 0, but expands to nothing knowable.
	//   np${X}m publish       from word 0 and resolves to a real name, `npm`
	//                         — but only because the empty environment assumed
	//                         ${X} was empty. At runtime it could be anything.
	//
	// The last is why literalness is the test rather than provenance. Checking
	// only which word a field came from would pass `np${X}m` and report a
	// program that was inferred from an assumption about the environment.
	//
	// An argument reported as a program is a rule firing on a command that
	// never ran it — the one outcome worse than missing it.
	if !fields[0].literal {
		return nil
	}

	argv := make([]string, 0, len(fields))
	for _, f := range fields {
		if f.lost {
			// An unresolvable argument is omitted rather than guessed at or
			// given a placeholder. The program is known; this one word is not.
			continue
		}
		argv = append(argv, f.value)
	}
	if len(argv) == 0 || argv[0] == "" {
		// A word can expand to an empty string — `"$EDITOR" file.txt` with
		// EDITOR unset. An empty program name is not a program.
		return nil
	}

	return fromArgv(argv)
}
