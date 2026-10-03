package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The Stop's header is printed once: a range several rules refused is one item whose rules are its
// sub-items, never a second header nested in the first.
func TestJoinRefusals_RangesAreSubItemsUnderOneHeader(t *testing.T) {
	r1 := rangeRefusal("In /a (main, from abc)", []string{"first rule objects", "second rule objects"})
	r2 := rangeRefusal("In /b (feat, from def)", []string{"third rule objects"})

	got := joinRefusals([]string{r1, r2})
	assert.Equal(t, 1, strings.Count(got, "rules refused"), got)
	assert.Contains(t, got, "the following rules refused this turn's work:\n  - In /a (main, from abc):\n    - first rule objects\n    - second rule objects\n  - In /b (feat, from def): third rule objects")

	alone := joinRefusals([]string{r1})
	assert.Equal(t, 1, strings.Count(alone, "rules refused"), "a lone range of several rules still lists them under the header: %s", alone)
	assert.Contains(t, alone, "\n    - first rule objects")

	assert.Equal(t, "In /b (feat, from def): third rule objects", joinRefusals([]string{r2}), "one range, one rule: no header")
}
