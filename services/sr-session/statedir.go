package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/sloprail/sloprail/internal/gitrepo"
)

// AppName is the directory this tool keeps its own data under.
const AppName = "sloprail"

// GuardrailEnv names the guardrail whose hook is running.
//
// Which guardrail is asking is never a parameter to `session state`. The engine
// ran the hook and knows, and it tells the hook by putting it here rather than
// in the argument vector, where a hook could write a different name and read a
// rule it was never told about — and then depend on when that rule ran.
const GuardrailEnv = "SR_GUARDRAIL"

// SessionEnv names the session a hook belongs to, for the same reason.
const SessionEnv = "SR_SESSION_ID"

// WorkspaceEnv is the tree the session is guarding. A hook runs with its own
// working directory set to the guardrail's folder, so the process's cwd is not
// the workspace and cannot stand in for it.
const WorkspaceEnv = "SR_WORKSPACE"

// TranscriptEnv is the path of the record this session is writing, for a rule
// that reads the trajectory rather than the pending call.
//
// SessionEnv cannot stand in for it: that is the STABLE id, the uuid of the
// conversation's origin record, deliberately not the harness's current id and
// therefore not the transcript's filename. Unset when the payload named no
// record — see transcriptEnv for why this one is omitted where the workspace is
// given a sentinel instead.
const TranscriptEnv = "SR_TRANSCRIPT"

// GuardrailDirEnv is the folder the running guardrail was declared in — the same
// path the hook's payload already carries as `guardrailDir`.
//
// Duplicated into the environment because reading it from the payload costs a
// `jq` invocation to answer "where am I", and a script that must parse something
// in order to find its own siblings has to find its parser first. The hook's cwd
// is this same directory, so a rule needing only a sibling can use a relative
// path; this exists for the rules that hand an ABSOLUTE path to something else —
// `sr-file validate --schema` above all — where a relative path would resolve
// against the callee's directory instead of the rule's.
const GuardrailDirEnv = "SR_GUARDRAIL_DIR"

// PluginRootEnv is the installation directory of the plugin the running
// guardrail shipped inside, and is UNSET for a guardrail the project wrote.
//
// What it is for: a shipped rule's assets — a CUE schema, a prompt, a helper
// script — travel with the plugin, not with the consumer's tree. A rule naming
// `$SR_WORKSPACE/.sloprail/schemas/task.cue` works in the repo it was written in
// and fails everywhere it is installed, because the consumer never had that
// file. This names where the assets actually landed.
//
// Unset rather than empty, so `${SR_PLUGIN_ROOT:-$SR_WORKSPACE/.sloprail}`
// selects the project's layout by the ordinary shell idiom — see pluginRootEnv,
// which records what a set-but-empty value would break and what the unset one
// leaves open.
//
// Deliberately NOT Claude Code's CLAUDE_PLUGIN_ROOT. That variable is set only
// for a hook the plugin itself registered, so a plugin shipping guardrails and
// registering no hooks would never see it — discovery would become a property of
// what happened to RUN rather than of what the project INSTALLED. The engine
// resolves the installation from the project's settings instead, and this is
// that answer handed on. See marketplace/plugins/sloprail's README, "Why
// discovery is not done from here".
const PluginRootEnv = "SR_PLUGIN_ROOT"

// dataHome is the platform's directory for data a program keeps between runs.
//
// Deliberately outside the guarded project: state written into the tree would
// show up in the very diffs the engine reads, and in the user's git status.
func dataHome() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sloprail: locate home directory: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support"), nil
	case "windows":
		if dir := os.Getenv("LocalAppData"); dir != "" {
			return dir, nil
		}
		return filepath.Join(home, "AppData", "Local"), nil
	default:
		// The XDG default, which is also the sensible answer anywhere else.
		return filepath.Join(home, ".local", "share"), nil
	}
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// encodeWorkspace turns a working directory into one path component, the way
// Claude Code encodes one for its own transcripts.
//
// A 1:1 substitution rather than a hash: the same reasoning a10n records, that
// a directory a person can read is worth more when they are looking at their
// own state than the shorter name would be. Symlinks are resolved first because
// macOS reports /var where the filesystem holds /private/var, and the two would
// otherwise be two different sessions of the same tree.
func encodeWorkspace(dir string) string {
	return nonAlnum.ReplaceAllString(workspaceAnchor(dir), "-")
}

// workspaceAnchor is the tree a directory belongs to: its git root where there
// is one, and the directory itself where there is not.
//
// Taken from a10n, which anchors a session on `git_root` rather than on the
// directory a hook happened to run in, and says why: a caller matches "by just
// resolving its OWN cwd's git root the same way, no repo-id translation
// needed". The anchor has to be a property of the TREE, or two callers who are
// demonstrably in the same tree disagree about where its state lives.
//
// Without it the engine keys on the raw cwd, and a hook invoked from a
// SUBDIRECTORY keys somewhere else entirely — a different database, an empty
// baseline, and every verdict the session had recorded suddenly unreachable,
// silently and mid-session. Nothing about that is specific to sub-agents; it is
// the plain case of an agent that ran `cd internal && …`, and it is the same
// class of silent orphaning StableSessionID exists to prevent one level up.
//
// It also turns the sub-agent story from an accident into a decision. An
// isolated sub-agent gets its own state because a linked worktree has its OWN
// git root — `rev-parse --show-toplevel` answers the worktree, not the main
// checkout — so it keys elsewhere BECAUSE it is a different tree, which is the
// reason we wanted. A shared-tree sub-agent resolves to the same anchor as its
// parent, from any subdirectory either of them runs in, and is separated by the
// session component alone. Before this, both of those held only as long as
// nobody ran a hook from a subdirectory.
//
// Symlinks are resolved on the fallback because macOS reports /var where the
// filesystem holds /private/var, and the two would otherwise be two different
// trees. git's own answer is already resolved, so it is taken as it comes.
//
// A directory that is not in a repository is its own anchor. A project without
// git is one the engine guards with everything except the difference, rather
// than one it refuses to key state for at all — the same choice ensureBaseline
// makes about baselineUnavailable.
func workspaceAnchor(dir string) string {
	if root, err := gitrepo.Root(dir); err == nil && root != "" {
		return root
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// sessionDBPath resolves where one session's state lives:
//
//	{data home}/sloprail/sessions/{workspace}/{session}/state.db
//
// Per workspace as well as per session because a session may hold its own
// worktree, and one tree's verdicts must not stand in for another's. Resolved
// here rather than in the store: the store is root-agnostic, which is what lets
// a test point it at a temporary directory.
//
// A SUB-AGENT needs nothing added to this, and that is the test the design had
// to pass rather than a convenience. A sub-agent is a session in its own right,
// so it arrives here with its own session id — resolved from its own origin
// record, distinct from its parent's — and with its own cwd, which is its
// isolated worktree when it was dispatched into one and the parent's tree when
// it was not. Both coordinates are already the ones this path is built from, so
// a sub-agent keys somewhere else by the same rule that placed the parent's
// state, not by a special case. Had a sub-agent needed a mechanism the root does
// not, the scheme would have been the wrong one.
//
// What that yields, in the two cases:
//
//   - Own worktree: a different workspace AND a different session, so nothing is
//     shared. Correct, and the stronger of the two requirements — the sub-agent
//     is judging different content at the same repository-relative paths, so a
//     pass it records is not a statement about the parent's file at all. Pooling
//     them would exempt the parent from a check on bytes nobody looked at.
//
//   - Parent's worktree: the same workspace, still a different session. So the
//     two collide in the workspace component and separate in the session one.
//     True from any subdirectory either of them runs a hook in, which it was
//     not before the anchor moved to the git root — see workspaceAnchor.
//
// # What this takes from a10n's session_folders, and what it does not
//
// a10n keys ONE session against MANY folders: a `session_folders` table on
// (cwd_hash, provider, session_id, path), each row carrying its own branch,
// base_ref and head_ref, so a session working across an isolated worktree and a
// shared tree tracks each independently.
//
// The grain is already ours. `{workspace}/{session}` IS that composite key with
// the folder in it — a session working across two trees has two databases,
// tracking two baselines, exactly as a10n has two rows. What was genuinely
// missing was the ANCHOR: a10n keys folders on the git root and we keyed on the
// raw cwd, which is why an isolated sub-agent got its own state by accident of
// path spelling rather than because it is a separate tree. That is now
// workspaceAnchor's job, and it is the one thing taken here.
//
// Two things are deliberately NOT taken.
//
// A `role` column. a10n needs one because its folders differ in KIND — a spec
// clone and an impl checkout are governed by different checks, and something
// has to tell them apart. Ours differ only in which tree they are, which the
// anchor already says. A role would be a field with one legal value, and a rule
// able to read it would be a rule able to behave differently for a sub-agent —
// the special case this design exists to avoid.
//
// An enumeration across a session's folders. a10n's ListByGitRoot exists to
// answer "what is this session working on", for a person and for a dispatcher
// that drains folders it did not run in. Nothing here asks that: every hook is
// invoked in one tree and judges that tree, and a query that could reach across
// folders would be a way for one agent's cycle to read another's — the boundary
// the paragraphs below are about. Worth revisiting the day something needs to
// report on a session as a whole; it is not needed to guard one.
//
// That second case is the one worth arguing, because sharing the tree is a real
// argument for sharing the verdicts: the fingerprint exists to avoid re-judging
// content already judged, and here the content genuinely is the same bytes.
// They are still kept apart, for two reasons.
//
// A verdict is not only about content. It is about content AS JUDGED BY A RULE,
// which is why the spec keeps them per guardrail rather than per file: a file
// one rule passed is a file another may never have seen. The same reasoning
// carries across sessions — a judge check is a model call, not a pure function,
// so a pass is one judgement by one agent, and the guardrail state a rule keeps
// alongside it ("I already warned about X") is a fact about a conversation
// rather than about the tree. Letting a sub-agent inherit that would let it
// skip a warning it was never given, and the spec's reasoning that no rule may
// read another rule's keyspace is the same boundary one step out: crossing it
// between agents would let one read work it was never told about.
//
// And the cost of the two choices is not symmetric. Keeping them apart costs a
// re-judgement of unchanged content — a second look, paid once. Pooling them
// risks exempting a file nothing judged, which loses a violation for good. Where
// one error is recoverable and the other is not, the recoverable one is the one
// to take.
func sessionDBPath(cwd, sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("sloprail: no session id — set %s", SessionEnv)
	}
	// A session id is a name, never a path. It is joined onto the state root
	// below, and filepath.Join CLEANS after concatenating, so "../../elsewhere"
	// resolves out of this workspace's directory and into another project's
	// state — read and written with no error raised anywhere. This is not
	// hypothetical on this codebase: an id reaching another project's state is a
	// bug that has already shipped here once.
	//
	// Refused rather than repaired. filepath.Base would make the path safe and
	// the anomaly invisible, and an id that is not a name means something
	// upstream is wrong in a way worth hearing about. Both separators are
	// refused because Windows accepts both; "." and ".." are named explicitly
	// because they traverse while containing no separator.
	if strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." {
		return "", fmt.Errorf("sloprail: %q is not a session id: it would resolve outside this session's own state", sessionID)
	}
	root, err := dataHome()
	if err != nil {
		return "", err
	}
	if cwd == unresolvedWorkspace {
		// The engine ran this hook but could not say which tree it guards. The
		// fallback below must not be reached here: a hook's process directory is
		// the guardrail's own folder, so falling back would key this rule's state
		// by where its scripts live and hand every rule a private database.
		return "", errUnresolvedWorkspace()
	}
	if cwd == "" {
		// Reached from a person running the CLI by hand, where the process's own
		// directory IS the tree they mean. A hook never arrives here: the engine
		// always sets the variable, to the sentinel above when it has no answer.
		if cwd, err = os.Getwd(); err != nil {
			return "", fmt.Errorf("sloprail: locate working directory: %w", err)
		}
	}
	return filepath.Join(root, AppName, "sessions", encodeWorkspace(cwd), sessionID, "state.db"), nil
}
