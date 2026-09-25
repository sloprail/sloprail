This mock/emulator already fires a SessionStart hook with source="compact"
after context compaction happens (see claude-mock/internal/runner/stream.go
around the isCompactSummary handling). Real Claude Code also fires a
separate PreCompact hook BEFORE compaction actually happens, and this mock
doesn't emit that yet. Add PreCompact support: fire it right before the
mock reacts to an isCompactSummary record, with a matcher that
distinguishes manual compaction from automatic compaction the same way the
existing hook events in internal/hooks/event.go are structured. Keep it
consistent with how the other hook events in this codebase are
implemented.
