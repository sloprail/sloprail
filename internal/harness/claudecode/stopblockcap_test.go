package claudecode

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/internal/harness"
)

func TestStopBlockCap(t *testing.T) {
	assert.Equal(t, 8, harness.StopBlockCap(Harness{}), "Claude Code overrides a Stop hook after 8 consecutive blocks")
}
