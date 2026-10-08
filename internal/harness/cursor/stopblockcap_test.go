package cursor

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/internal/harness"
)

func TestStopBlockCap(t *testing.T) {
	assert.Equal(t, 0, harness.StopBlockCap(Harness{}), "Cursor's non-interactive Stop hook never blocks, so there is no cap")
}
