package guardrail

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two matchers under one event kind — the case hit while writing the first
// worked example. yaml.v3 refuses this on its own; what is asserted here is
// that the report names the key's path and both lines rather than leaving an
// author to decode "mapping key already defined".
func TestDuplicateKeys_RepeatedEventKind(t *testing.T) {
	dups, err := duplicateKeys([]byte(`hooks:
  PreFileCreate:
    - matcher: path startsWith "a/"
  PreFileCreate:
    - matcher: path startsWith "b/"
`))
	require.NoError(t, err)
	require.Len(t, dups, 1)
	assert.ErrorIs(t, dups[0], ErrDuplicateKey)
	assert.Contains(t, dups[0].Message(), "hooks.PreFileCreate")
	assert.Contains(t, dups[0].Message(), "lines 2, 4", "both positions, so the author can find them")
}

func TestDuplicateKeys_RepeatedTopLevelKey(t *testing.T) {
	dups, err := duplicateKeys([]byte("enabled: true\nenabled: false\nhooks: {}\n"))
	require.NoError(t, err)
	require.Len(t, dups, 1)
	assert.ErrorIs(t, dups[0], ErrDuplicateKey)
	assert.Contains(t, dups[0].Message(), "enabled")
}

// Depth does not change the report. Two commands in one hook is the same
// collision one level further down, and is named the same way.
func TestDuplicateKeys_RepeatedKeyInsideAHook(t *testing.T) {
	dups, err := duplicateKeys([]byte(`hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./a.sh
          command: ./b.sh
`))
	require.NoError(t, err)
	require.Len(t, dups, 1)
	assert.ErrorIs(t, dups[0], ErrDuplicateKey)
	assert.Contains(t, dups[0].Message(), "command")
}

// Every duplicate, not the first. This is the one thing the library cannot do:
// an unmarshal error ends the document, so it reports one and stops.
func TestDuplicateKeys_ReportsAllOfThem(t *testing.T) {
	dups, err := duplicateKeys([]byte(`enabled: true
enabled: false
hooks:
  PreFileCreate:
    - matcher: a
  PreFileCreate:
    - matcher: b
`))
	require.NoError(t, err)
	assert.Len(t, dups, 2)
}

// The acceptance half. A declaration binding several kinds, and several
// bindings under one kind, repeats key names legitimately — `matcher` appears
// once per binding — and none of that is a duplicate.
func TestDuplicateKeys_AcceptsLegitimateRepetition(t *testing.T) {
	dups, err := duplicateKeys([]byte(`enabled: true
hooks:
  PreFileCreate:
    - matcher: path startsWith "a/"
      hooks:
        - type: command
          command: ./a.sh
    - matcher: path startsWith "b/"
      hooks:
        - type: command
          command: ./b.sh
  PreFileUpdate:
    - matcher: path endsWith ".md"
      hooks:
        - type: command
          command: ./c.sh
`))
	require.NoError(t, err)
	assert.Empty(t, dups, "the same key in sibling mappings is not a duplicate")
}

func TestDuplicateKeys_EmptyFrontmatter(t *testing.T) {
	dups, err := duplicateKeys(nil)
	require.NoError(t, err)
	assert.Empty(t, dups)
}

func TestDuplicateKeys_MalformedYAMLIsReported(t *testing.T) {
	_, err := duplicateKeys([]byte("hooks:\n  - : :\n   bad\n"))
	assert.Error(t, err)
}
