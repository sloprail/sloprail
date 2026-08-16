package main

import "github.com/sloprail/sloprail/internal/statedir"

// The state directory's vocabulary, re-exported under the names this package
// has always used.
//
// The definitions moved to internal/statedir because a second binary needs
// them: `sr-file checks` opens the same per-session store, and where that store
// lives — the platform data dir, the workspace encoding, the refusal to treat a
// session id as a path — is one answer or it is two answers that will disagree.
// A copy in each binary is how two commands come to key the same session
// differently, and then to look at each other's empty registers.
//
// Aliased rather than rewritten at every call site so the move stays a move: the
// diff shows one file changing hands, not fifty lines of renaming that have to
// be read to be trusted.
const (
	AppName         = statedir.AppName
	GuardrailEnv    = statedir.GuardrailEnv
	SessionEnv      = statedir.SessionEnv
	WorkspaceEnv    = statedir.WorkspaceEnv
	TranscriptEnv   = statedir.TranscriptEnv
	GuardrailDirEnv = statedir.GuardrailDirEnv
	PluginRootEnv   = statedir.PluginRootEnv
)

var (
	sessionDBPath          = statedir.SessionDBPath
	dataHome               = statedir.DataHome
	encodeWorkspace        = statedir.EncodeWorkspace
	workspaceAnchor        = statedir.WorkspaceAnchor
	errUnresolvedWorkspace = statedir.ErrUnresolvedWorkspace
)

// UnresolvedWorkspace is a const rather than a var, so it is aliased separately.
const unresolvedWorkspace = statedir.UnresolvedWorkspace

// encodePath is how a directory becomes one path component, for the tests that
// build the transcript path the harness would. The regexp behind it stays
// private to statedir: a caller wanting the substitution wants the ANSWER, and
// exporting the pattern would invite a second implementation of it.
var encodePath = statedir.EncodePath
