package harness

// StopBlockCapper is what a Harness MAY implement to say how many times in a row
// it lets a Stop hook block before it overrides the hook and ends the turn itself
// (Claude Code's CLAUDE_CODE_STOP_HOOK_BLOCK_CAP, default 8). 0 means the harness
// has no such cap.
type StopBlockCapper interface {
	StopBlockCap() int
}

// StopBlockCap is h's own cap on consecutive Stop blocks, 0 when h implements no
// StopBlockCapper: Codex keeps going through 30 blocks in a row, and Cursor's
// non-interactive Stop hook never blocks the turn, so neither has a cap to mirror.
func StopBlockCap(h Harness) int {
	if c, ok := h.(StopBlockCapper); ok {
		return c.StopBlockCap()
	}
	return 0
}
