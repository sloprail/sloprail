package harness

// The canonical tool vocabulary.
//
// Every harness spells its tools its own way; the engine's modules (filemod,
// commandmod, tag, ...) read tool names and arguments in ONE spelling, the one
// Claude Code established, and an adapter's ParseHook maps its harness onto it.
// A tool a harness has that is not below is passed through under its own name and
// is simply not a write or a command as far as any module knows.
//
//	Bash          a shell command; arguments {"command": "<line>"}.
//	Write         create or overwrite a file; {"file_path", "content"}.
//	Edit          replace text in a file; {"file_path", "old_string", "new_string", "replace_all"?}.
//	MultiEdit     several sequential Edits; {"file_path", "edits": [{"old_string","new_string"}...]}.
//	NotebookEdit  edit a notebook cell; {"notebook_path", "new_source", ...}.
//	Read          read a file; {"file_path"}. Never a write.
//
// A harness whose write tool cannot be expressed in those argument shapes (a
// multi-file patch) reports the effects in HookInput.Files instead and keeps
// its own tool name.
//
// These are the keys commandmod.HarnessCommandTools and HarnessWriteTools hold; a
// test there pins the two together.
const (
	ToolBash         = "Bash"
	ToolWrite        = "Write"
	ToolEdit         = "Edit"
	ToolMultiEdit    = "MultiEdit"
	ToolNotebookEdit = "NotebookEdit"
	ToolRead         = "Read"
)
