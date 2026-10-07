package harness

// ProjectMarkerer is what a Harness MAY implement to name the directories in which it
// records what a project installed (Claude Code's `.claude`, Cursor's `.cursor`): the
// project root is the nearest directory holding one. A harness without it has no
// marker, and its project root is the workspace anchor.
type ProjectMarkerer interface {
	ProjectMarkers() []string
}

// ProjectMarkers is h's project marker directory names; none when h does not say.
func ProjectMarkers(h Harness) []string {
	if m, ok := h.(ProjectMarkerer); ok {
		return m.ProjectMarkers()
	}
	return nil
}
