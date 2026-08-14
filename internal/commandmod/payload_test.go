package commandmod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// payloadFor returns the payload of the single write target on a line, for the
// many cases that have exactly one.
//
// It asserts there IS exactly one write target, so a case that accidentally
// stops naming a file fails as a missing target rather than silently passing as
// a missing payload.
func payloadFor(t *testing.T, line string) Payload {
	t.Helper()
	var writes []FileTarget
	for _, tg := range FileTargets(line) {
		if tg.Effect == Write {
			writes = append(writes, tg)
		}
	}
	require.Len(t, writes, 1, "line %q must name exactly one written file", line)
	return writes[0].Payload
}

// --- literal output ---------------------------------------------------------

// TestPayload_EchoRendersItsOutputExactly is the newline case, and the newline
// is the whole point.
//
// `echo hello` writes "hello\n". A payload reporting "hello" would be wrong by
// one byte, which is invisible in a diff and fatal to a fingerprint: a rule
// hashing the resulting file, or asking whether it ends in a newline, gets the
// wrong answer for every echo on the line.
func TestPayload_EchoRendersItsOutputExactly(t *testing.T) {
	for line, want := range map[string]string{
		"echo hello > f.md":           "hello\n",
		"echo -n hello > f.md":        "hello",
		"echo one two three > f.md":   "one two three\n",
		"echo > f.md":                 "\n",
		"echo -n > f.md":              "",
		`echo "quoted words" > f.md`:  "quoted words\n",
		`echo 'single quoted' > f.md`: "single quoted\n",
	} {
		t.Run(line, func(t *testing.T) {
			p := payloadFor(t, line)
			assert.Equal(t, PayloadLiteral, p.Kind)
			assert.Equal(t, want, p.Text)
		})
	}
}

// TestPayload_EchoWithEscapeFlagsIsNotClaimed is the deliberate limit.
//
// `-e` enables backslash escapes, but which escapes and whether they are on by
// default differ between bash's builtin, dash's, and /bin/echo. So the
// resulting bytes depend on the interpreter rather than the line, and this
// package does not know the interpreter.
//
// Declining costs a rule that does not see the content. Guessing would produce
// bytes the file never holds, which is the failure this whole change exists to
// remove.
func TestPayload_EchoWithEscapeFlagsIsNotClaimed(t *testing.T) {
	for _, line := range []string{
		`echo -e 'a\tb' > f.md`,
		`echo -E 'a\tb' > f.md`,
		`echo -e > f.md`,
	} {
		assert.Equal(t, PayloadNone, payloadFor(t, line).Kind,
			"line %q depends on which shell runs it", line)
	}
}

// TestPayload_EchoOfANonLiteralWordIsNotClaimed holds the certainty rule at the
// content level.
//
// `echo $GREETING > f.md` names the file perfectly and says nothing about the
// bytes: resolving it means assuming the empty environment is the real one,
// which safeConfig refuses for exactly this reason. So the PATH is reported and
// the CONTENT is not — the two halves of one line, each as strict as its own
// answer needs to be.
func TestPayload_EchoOfANonLiteralWordIsNotClaimed(t *testing.T) {
	targets := FileTargets("echo $GREETING > f.md")
	require.Len(t, targets, 1, "the redirection still names the file")
	assert.Equal(t, "f.md", targets[0].Path)
	assert.Equal(t, PayloadNone, targets[0].Payload.Kind,
		"the path is certain and the content is not, and they are judged separately")
}

// TestPayload_PrintfIsRenderedOnlyWhereItIsExact draws the line deliberately.
//
// printf is a small language, and reimplementing it would mean numeric
// conversion, width and precision, and escapes that vary. The subset handled is
// the one that appears in real scripts and is unambiguous; everything else
// declines, which costs a missing content rather than a wrong one.
func TestPayload_PrintfIsRenderedOnlyWhereItIsExact(t *testing.T) {
	t.Run("rendered", func(t *testing.T) {
		for line, want := range map[string]string{
			// No conversions: the format IS the output, and printf adds no
			// trailing newline of its own. That is the difference from echo.
			"printf 'no newline' > f.md":  "no newline",
			`printf 'with\n' > f.md`:      "with\n",
			`printf 'a\tb' > f.md`:        "a\tb",
			`printf '%s' hello > f.md`:    "hello",
			`printf '%s\n' hello > f.md`:  "hello\n",
			`printf 'x: %s\n' val > f.md`: "x: val\n",
		} {
			t.Run(line, func(t *testing.T) {
				p := payloadFor(t, line)
				assert.Equal(t, PayloadLiteral, p.Kind)
				assert.Equal(t, want, p.Text)
			})
		}
	})

	t.Run("declined", func(t *testing.T) {
		for _, line := range []string{
			// A conversion this does not model.
			`printf '%d' 5 > f.md`,
			`printf '%5s' x > f.md`,
			// Two conversions, or a format reused across surplus arguments —
			// printf LOOPS, which is not modelled.
			`printf '%s %s' a b > f.md`,
			`printf '%s\n' a b c > f.md`,
			// A literal format with surplus arguments loops too.
			`printf 'x' a > f.md`,
			// An escape form this declines rather than approximates.
			`printf '\x41' > f.md`,
			`printf '\101' > f.md`,
		} {
			assert.Equal(t, PayloadNone, payloadFor(t, line).Kind,
				"line %q is not exactly renderable", line)
		}
	})
}

// TestPayload_TruncatingCommandsProduceARealEmpty is the tier where "" is a
// FACT rather than a stand-in for ignorance.
//
// This is the distinction the whole change turns on. `: > f.md` genuinely
// empties the file, and a rule written `content == ""` should fire on it — as
// opposed to `unknown-tool > f.md`, where the same empty string would be a lie.
func TestPayload_TruncatingCommandsProduceARealEmpty(t *testing.T) {
	for _, line := range []string{
		": > f.md",
		"true > f.md",
		"touch f.md",
		"truncate -s 0 f.md",
		"truncate -s0 f.md",
		"truncate --size=0 f.md",
		"truncate --size 0 f.md",
	} {
		p := payloadFor(t, line)
		assert.Equalf(t, PayloadLiteral, p.Kind, "line %q determines an empty file", line)
		assert.Equalf(t, "", p.Text, "line %q", line)
	}
}

// TestPayload_TruncateToANonZeroSizeIsNotClaimed is the boundary of the case
// above. Any size but zero depends on the file's current length — padding with
// NULs, or cutting at an offset — and the relative forms depend on it twice.
func TestPayload_TruncateToANonZeroSizeIsNotClaimed(t *testing.T) {
	for _, line := range []string{
		"truncate -s 100 f.md",
		"truncate -s +10 f.md",
		"truncate -s -10 f.md",
		"truncate --size=1K f.md",
		"truncate -r other.md f.md",
	} {
		assert.Equalf(t, PayloadNone, payloadFor(t, line).Kind,
			"line %q depends on the file's current bytes", line)
	}
}

// TestPayload_TouchIsMarkedMTimeOnly pins the flag that stops a create-shaped
// payload from lying about an existing file.
//
// touch determines an empty file when it CREATES one and changes nothing when
// the file is already there. This package cannot tell which without reading the
// tree, so it states both halves and the caller picks — see FileTarget.MTimeOnly.
func TestPayload_TouchIsMarkedMTimeOnly(t *testing.T) {
	targets := FileTargets("touch a.md b.md")
	require.Len(t, targets, 2)
	for _, tg := range targets {
		assert.True(t, tg.MTimeOnly, "touch changes no bytes when the file exists")
		assert.Equal(t, PayloadLiteral, tg.Payload.Kind)
		assert.Equal(t, "", tg.Payload.Text)
	}
}

// TestPayload_OnlyTouchIsMTimeOnly is the negative half, and it matters: a flag
// set on everything would suppress every computed result rather than just
// touch's.
func TestPayload_OnlyTouchIsMTimeOnly(t *testing.T) {
	for _, line := range []string{
		"echo hi > f.md",
		"cp a.md f.md",
		"truncate -s 0 f.md",
		": > f.md",
	} {
		for _, tg := range FileTargets(line) {
			assert.Falsef(t, tg.MTimeOnly, "line %q genuinely changes bytes", line)
		}
	}
}

// --- redirection operators --------------------------------------------------

// TestPayload_AppendIsDistinctFromTruncate is the operator distinction, and it
// changes the KIND rather than the text.
//
// `>` truncates, so the result is exactly the produced bytes. `>>` appends, so
// the result is the file's current bytes plus them — a base only the caller can
// read, which is why it is PayloadAppend rather than PayloadLiteral.
func TestPayload_AppendIsDistinctFromTruncate(t *testing.T) {
	truncating := payloadFor(t, "echo hi > f.md")
	assert.Equal(t, PayloadLiteral, truncating.Kind)
	assert.Equal(t, "hi\n", truncating.Text)

	appending := payloadFor(t, "echo hi >> f.md")
	assert.Equal(t, PayloadAppend, appending.Kind,
		"the base is the file itself, which this package does not read")
	assert.Equal(t, "hi\n", appending.Text, "the tail is still exact")
}

// TestPayload_ExoticWritingOperatorsClaimNothing holds the operators where the
// resulting bytes are genuinely not the command's stdout.
//
// `&>` merges stderr, whose contents are not knowable from the line. `<>` opens
// for read and write WITHOUT truncating and writes at an offset, so the result
// depends on the file's current bytes. Claiming the produced output for either
// would be wrong in a way no test would catch until it refused real work.
func TestPayload_ExoticWritingOperatorsClaimNothing(t *testing.T) {
	for _, line := range []string{
		"echo hi &> f.md",
		"echo hi &>> f.md",
		"echo hi <> f.md",
	} {
		assert.Equalf(t, PayloadNone, payloadFor(t, line).Kind,
			"line %q does not put exactly the command's stdout in the file", line)
	}
}

// --- here-documents ---------------------------------------------------------

// TestPayload_AQuotedHeredocIsLiteralAndAnUnquotedOneIsNot is the literalness
// rule applied to a here-document, and it is read off the DELIMITER rather than
// by scanning the body.
//
// Reading the delimiter is what the shell itself does, and it is right even for
// a quoted heredoc whose body happens to contain a dollar sign — which a body
// scan would wrongly refuse.
func TestPayload_AQuotedHeredocIsLiteralAndAnUnquotedOneIsNot(t *testing.T) {
	t.Run("quoted is verbatim", func(t *testing.T) {
		p := payloadFor(t, "cat > f.md <<'EOF'\nliteral $HOME stays\nEOF\n")
		assert.Equal(t, PayloadLiteral, p.Kind)
		assert.Equal(t, "literal $HOME stays\n", p.Text,
			"a quoted delimiter means the body is taken exactly, dollar signs and all")
	})

	t.Run("double-quoted delimiter is also verbatim", func(t *testing.T) {
		p := payloadFor(t, "cat > f.md <<\"EOF\"\nliteral $HOME\nEOF\n")
		assert.Equal(t, PayloadLiteral, p.Kind)
		assert.Equal(t, "literal $HOME\n", p.Text)
	})

	t.Run("unquoted interpolates, so it is not knowable", func(t *testing.T) {
		p := payloadFor(t, "cat > f.md <<EOF\nvalue is $HOME\nEOF\n")
		assert.Equal(t, PayloadNone, p.Kind,
			"the body expands against an environment this package does not have")
	})
}

// TestPayload_ADashHeredocStripsLeadingTabsOnly models `<<-` exactly.
//
// The stripping is tabs only and leading only — that is the specification, and
// a version stripping whitespace generally would report bytes the shell does
// not produce.
func TestPayload_ADashHeredocStripsLeadingTabsOnly(t *testing.T) {
	p := payloadFor(t, "cat > f.md <<-'EOF'\n\ttabbed\n\t\tdouble\n  spaced\nEOF\n")
	assert.Equal(t, PayloadLiteral, p.Kind)
	assert.Equal(t, "tabbed\ndouble\n  spaced\n", p.Text,
		"leading tabs go, leading spaces stay")
}

// TestPayload_AHeredocIntoANonPassThroughProgramIsNotClaimed is the case that
// keeps `cat` from being a general theory of stdin.
//
// A here-document is the program's INPUT. Only a program that copies stdin to
// stdout lets that input stand as the redirection's output — `sort` reorders
// it, `wc` counts it, and reporting the document as the resulting file would be
// wrong for both.
func TestPayload_AHeredocIntoANonPassThroughProgramIsNotClaimed(t *testing.T) {
	for _, line := range []string{
		"sort > f.md <<'EOF'\nb\na\nEOF\n",
		"wc -l > f.md <<'EOF'\na\nEOF\n",
		// cat WITH operands concatenates those files rather than passing stdin
		// through, so the heredoc is not the whole output.
		"cat other.md > f.md <<'EOF'\nx\nEOF\n",
	} {
		assert.Equalf(t, PayloadNone, payloadFor(t, line).Kind,
			"line %q transforms its input", line)
	}
}

// TestPayload_AHeredocIntoTeeIsTheFilesContent covers the stdin-consuming
// programs, whose target comes from the known-binary table rather than from a
// redirection.
//
// `tee f.md <<'EOF'` names its file as an OPERAND while the bytes hang off the
// statement, so neither node answers alone — see stmtHdoc.
func TestPayload_AHeredocIntoTeeIsTheFilesContent(t *testing.T) {
	p := payloadFor(t, "tee f.md <<'EOF'\nwritten by tee\nEOF\n")
	assert.Equal(t, PayloadLiteral, p.Kind)
	assert.Equal(t, "written by tee\n", p.Text)
}

// TestPayload_TeeWithoutAHeredocClaimsNothing is the negative that keeps the
// case above honest. tee's bytes normally come from a pipe, which the line does
// not carry.
func TestPayload_TeeWithoutAHeredocClaimsNothing(t *testing.T) {
	assert.Equal(t, PayloadNone, payloadFor(t, "tee f.md").Kind)
	assert.Equal(t, PayloadNone, payloadFor(t, "generate | tee f.md").Kind)
}

// --- copies -----------------------------------------------------------------

// TestPayload_CopyingNamesItsSourceRatherThanReadingIt is the seam, stated as a
// test: the payload REFERS to the source, and resolving it is the caller's.
//
// This is what keeps FileTargets a pure function of a string. A version that
// read a.md here would need a cwd and a filesystem, and every test in this
// package would need a temp directory.
func TestPayload_CopyingNamesItsSourceRatherThanReadingIt(t *testing.T) {
	for _, line := range []string{"cp a.md b.md", "mv a.md b.md"} {
		t.Run(line, func(t *testing.T) {
			var dst FileTarget
			for _, tg := range FileTargets(line) {
				if tg.Effect == Write {
					dst = tg
				}
			}
			require.Equal(t, "b.md", dst.Path)
			assert.Equal(t, PayloadCopyOf, dst.Payload.Kind)
			assert.Equal(t, "a.md", dst.Payload.From,
				"the source is NAMED, not read: reading it is filemod's half")
			assert.Empty(t, dst.Payload.Text, "a reference carries no bytes")
		})
	}
}

// TestPayload_MovingStillRemovesItsSource holds the half a payload must not
// displace. `mv a.md b.md` is a removal AND a write, and the removal carries no
// payload because a deleted file has no resulting content.
func TestPayload_MovingStillRemovesItsSource(t *testing.T) {
	var removed []FileTarget
	for _, tg := range FileTargets("mv a.md b.md") {
		if tg.Effect == Remove {
			removed = append(removed, tg)
		}
	}
	require.Len(t, removed, 1)
	assert.Equal(t, "a.md", removed[0].Path)
	assert.Equal(t, PayloadNone, removed[0].Payload.Kind,
		"a deletion has no resulting content, and PreFileDelete declares no field for one")
}

// TestPayload_CopyingIntoADirectoryClaimsNothing is the limit the path half
// already draws, held at the content level too.
//
// With three or more operands the last is a DIRECTORY and the resulting file is
// `dir/base(src)` — a path this package deliberately does not compute, because
// it depends on whether the directory exists. Claiming a payload for the
// directory would attach one file's bytes to a path that is not that file.
func TestPayload_CopyingIntoADirectoryClaimsNothing(t *testing.T) {
	for _, line := range []string{
		"cp a.md b.md target-dir",
		"mv a.md b.md target-dir",
	} {
		var dst FileTarget
		for _, tg := range FileTargets(line) {
			if tg.Effect == Write {
				dst = tg
			}
		}
		require.Equal(t, "target-dir", dst.Path, "line %q", line)
		assert.Equalf(t, PayloadNone, dst.Payload.Kind,
			"line %q copies INTO a directory, and the resulting filename is not computed", line)
	}
}

// --- the unknowable tier ----------------------------------------------------

// TestPayload_AnUnknownProgramClaimsNothing is the case the whole design is
// measured against.
//
// The redirection is real and the file is named, so the event still happens.
// The bytes are not knowable, and `content: ""` would make this
// indistinguishable from `touch new.md` — whose empty content is a fact. That
// collision is the defect this change removes, so reintroducing it here would
// undo the point.
func TestPayload_AnUnknownProgramClaimsNothing(t *testing.T) {
	for _, line := range []string{
		"some-unknown-tool > f.md",
		"python script.py > f.md",
		"make build > f.md",
		"./bin/generate > f.md",
		"curl https://example.com > f.md",
	} {
		targets := FileTargets(line)
		require.Lenf(t, targets, 1, "line %q still names its file", line)
		assert.Equalf(t, PayloadNone, targets[0].Payload.Kind,
			"line %q determines no bytes", line)
	}
}

// TestPayload_SedInPlaceClaimsNothing is a deliberate decision rather than an
// omission, and it is the one the brief asked to be argued either way.
//
// `sed -i 's/a/b/' f.md` states a TRANSFORMATION, not an outcome. Deriving the
// result would mean implementing sed's expression language — addresses, ranges,
// hold space, alternate delimiters, the `s` flags — against a file this package
// does not read, and then being byte-exact or being worse than useless.
//
// A narrow literal-substitution subset was considered and rejected. The subset
// that is genuinely safe (`s/literal/literal/` with no flags, no addresses, no
// regex metacharacters anywhere) is a small fraction of real sed usage, and the
// cost of getting the boundary wrong is a `result` that is confidently wrong —
// which is strictly worse than an honest "not known", because a rule cannot
// tell a wrong answer from a right one.
//
// So the answer is no, and `resultKnown` is what makes that answer sayable
// instead of silent. This is the case that field exists for.
func TestPayload_SedInPlaceClaimsNothing(t *testing.T) {
	for _, line := range []string{
		"sed -i 's/a/b/' f.md",
		"sed -i '' 's/a/b/' f.md",
		"sed -i.bak 's/a/b/' f.md",
		"perl -i -pe 's/a/b/' f.md",
	} {
		targets := FileTargets(line)
		require.NotEmptyf(t, targets, "line %q still names its file", line)
		for _, tg := range targets {
			assert.Equalf(t, PayloadNone, tg.Payload.Kind,
				"line %q states a transformation, not an outcome", line)
		}
	}
}

// TestPayload_DdClaimsNothingWithoutAHeredoc holds dd's ordinary case: its
// bytes come from `if=` or from stdin, neither of which the line's own text
// carries.
func TestPayload_DdClaimsNothingWithoutAHeredoc(t *testing.T) {
	assert.Equal(t, PayloadNone, payloadFor(t, "dd of=f.md").Kind)
	assert.Equal(t, PayloadNone, payloadFor(t, "dd if=other.md of=f.md").Kind)
}

// --- payloads survive the wrappers the path half already unwraps -------------

// TestPayload_SurvivesWrappersAndInterpreterPayloads is the property that keeps
// this from being evadable.
//
// The path half already unwraps `sudo` and re-parses `sh -c`, precisely so a
// rule cannot be dodged by prefixing a wrapper. The content half must reach the
// same depth, or `sh -c 'echo x > f.md'` would report the file with no bytes
// while the bare line reported both.
func TestPayload_SurvivesWrappersAndInterpreterPayloads(t *testing.T) {
	for _, line := range []string{
		"sudo tee f.md <<'EOF'\nbody\nEOF\n",
	} {
		p := payloadFor(t, line)
		assert.Equalf(t, PayloadLiteral, p.Kind, "line %q", line)
		assert.Equalf(t, "body\n", p.Text, "line %q", line)
	}

	t.Run("an interpreter payload", func(t *testing.T) {
		p := payloadFor(t, `sh -c 'echo hi > f.md'`)
		assert.Equal(t, PayloadLiteral, p.Kind,
			"the re-parsed payload must derive content too, or a wrapper hides it")
		assert.Equal(t, "hi\n", p.Text)
	})
}

// --- one line naming several files ------------------------------------------

// TestPayload_EachTargetOnALineKeepsItsOwnPayload holds that the payload is
// per-TARGET rather than per-line. `echo hi > a.md > b.md` sends the same bytes
// to both, while `echo hi > a.md; touch b.md` sends different ones.
func TestPayload_EachTargetOnALineKeepsItsOwnPayload(t *testing.T) {
	got := map[string]Payload{}
	for _, tg := range FileTargets("echo hi > a.md; touch b.md; cp src.md c.md") {
		if tg.Effect == Write {
			got[tg.Path] = tg.Payload
		}
	}
	require.Len(t, got, 3)

	assert.Equal(t, "hi\n", got["a.md"].Text)
	assert.Equal(t, PayloadLiteral, got["a.md"].Kind)

	assert.Equal(t, "", got["b.md"].Text)
	assert.Equal(t, PayloadLiteral, got["b.md"].Kind)

	assert.Equal(t, PayloadCopyOf, got["c.md"].Kind)
	assert.Equal(t, "src.md", got["c.md"].From)
}
