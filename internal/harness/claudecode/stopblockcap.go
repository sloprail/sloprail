package claudecode

// defaultStopBlockCap is Claude Code's own CLAUDE_CODE_STOP_HOOK_BLOCK_CAP default:
// after 8 consecutive Stop blocks it overrides the hook and ends the turn anyway.
const defaultStopBlockCap = 8

// StopBlockCap implements harness.StopBlockCapper.
func (Harness) StopBlockCap() int { return defaultStopBlockCap }
