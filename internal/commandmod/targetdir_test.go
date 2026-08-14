package commandmod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A flag that changes WHICH operand is the destination makes the line
// unclaimable, for cp and mv exactly as it always did for install.
//
// install refused `-t` from the start and its entry writes out why: reading the
// rest positionally is a measured wrong answer. cp and mv reach the same
// copyTargets by the same operand shape and never got the check, so both
// carried the defect that comment describes. Measured before this fix:
//
//	cp -t target-dir a.md b.md -> b.md Written, Into [target-dir a.md]
//	mv -t target-dir a.md b.md -> target-dir REMOVED, plus b.md Written
//
// The mv spelling is the worst of the three: a rule about deletions firing on
// the destination directory the command copies INTO.
func TestFileTargets_TargetDirectoryFlagMakesTheLineUnclaimable(t *testing.T) {
	for _, line := range []string{
		"cp -t target-dir a.md b.md",
		"cp --target-directory target-dir a.md b.md",
		"cp --target-directory=target-dir a.md b.md",
		"mv -t target-dir a.md b.md",
		"mv --target-directory=target-dir a.md b.md",
		"install -t target-dir a.md b.md",
		"install --target-directory=target-dir a.md b.md",

		// -T asserts the destination is NOT a directory, so the Into reading
		// copyTargets states is not merely unknown but false.
		"cp -T a.md b.md",
		"mv -T a.md b.md",
		"install -T a.md b.md",

		// Clustered short flags. install's own guard compared whole strings and
		// was defeated by exactly this — `-Dt` walked past `a == "-t"` and
		// inverted source and destination anyway.
		"cp -vt target-dir a.md b.md",
		"install -Dt target-dir a.md",
		"mv -ft target-dir a.md",
		"cp -vT a.md b.md",

		// install -d creates directories and copies nothing; it clusters too.
		"install -d a b",
		"install -dv a b",
	} {
		t.Run(line, func(t *testing.T) {
			assert.Empty(t, FileTargets(line),
				"a line whose destination is named by a flag must be claimed by nothing, "+
					"not read positionally")
		})
	}
}

// The boundary: ordinary copies are untouched, and `--` still protects an
// operand that merely LOOKS like the flag.
func TestFileTargets_OrdinaryCopiesAreUnaffectedByTheTargetDirectoryCheck(t *testing.T) {
	cases := []struct {
		line string
		path string
		from string
	}{
		{"cp a.md b.md", "b.md", "a.md"},
		{"cp -r src dst", "dst", "src"},
		{"cp -p a.md b.md", "b.md", "a.md"},
		{"install -m 644 a.md b.md", "b.md", "a.md"},
		{"cp -- a.md b.md", "b.md", "a.md"},
		// Everything after `--` is an operand, so this copies a file NAMED -t.
		{"cp -- -t b.md", "b.md", "-t"},
		// -S takes a separated value. Unskipped it slid into operand position
		// and `.bak` was reported among the sources copied into b.md.
		{"cp -S .bak a.md b.md", "b.md", "a.md"},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			targets := FileTargets(tc.line)
			require.NotEmpty(t, targets, "an ordinary copy must still be claimed")
			last := targets[len(targets)-1]
			assert.Equal(t, tc.path, last.Path)
			assert.Equal(t, []string{tc.from}, last.Payload.From,
				"the destination's bytes are the source's")
			assert.Equal(t, []string{tc.from}, last.Into,
				"and the directory reading names the same source")
		})
	}
}

// mv still reports its removals when the line IS claimable — the refusal above
// must not have cost the ordinary case.
func TestFileTargets_MoveStillReportsItsRemoval(t *testing.T) {
	targets := FileTargets("mv a.md b.md")
	require.Len(t, targets, 2)
	assert.Equal(t, "a.md", targets[0].Path)
	assert.Equal(t, Remove, targets[0].Effect)
	assert.Equal(t, "b.md", targets[1].Path)
}

// A flag's VALUE is not a file, for the same reason install has always skipped
// its own `-m 644`: an unskipped value slides into operand position and becomes
// a phantom target.
//
// The touch case was not merely an over-report. `touch -r ref.md a.md` produced
// a target for ref.md — a file the line only READS as an mtime reference —
// carrying touch's literal "" payload, which filemod turned into a
// PreFileCreate. A rule guarding creations fired on a file nothing writes.
func TestFileTargets_AFlagValueIsNotAFile(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"touch -r ref.md a.md", []string{"a.md"}},
		{"touch --reference ref.md a.md", []string{"a.md"}},
		{"touch --reference=ref.md a.md", []string{"a.md"}},
		{"touch -d now a.md", []string{"a.md"}},
		{"touch --date now a.md", []string{"a.md"}},
		// -t is a TIMESTAMP for touch and a target directory for cp: the same
		// two letters meaning different things, which is why each binary states
		// its own rather than sharing a table.
		{"touch -t 202601010000 a.md", []string{"a.md"}},
		{"mkdir -m 755 d", []string{"d"}},
		{"mkdir --mode 755 d", []string{"d"}},

		// The boundary: flags that take no value still leave their operands
		// alone, and `--` still protects an operand that looks like a flag.
		{"touch a.md", []string{"a.md"}},
		{"touch -c a.md", []string{"a.md"}},
		{"touch a.md b.md", []string{"a.md", "b.md"}},
		{"mkdir -p d", []string{"d"}},
		{"touch -- -r", []string{"-r"}},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			var got []string
			for _, tgt := range FileTargets(tc.line) {
				got = append(got, tgt.Path)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}
