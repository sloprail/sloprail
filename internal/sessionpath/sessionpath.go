// Package sessionpath is where a session's data lives, and who the session is.
//
// Both `sr-session` (which writes a session's state and check results) and
// `sr check` (which reads and writes the check results) have to find the SAME files, so
// the answer lives once, here: the platform's data directory, the encoding of a
// workspace, the per-session directory, and the session's stable identity — the
// uuid of where the conversation began, not the id the harness currently
// reports.
//
// Nothing here opens a database. The state store and the check-results store are
// their own packages' resources; this only says where they are.
package sessionpath

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/transcript"
)

// AppName is the directory this tool keeps its own data under.
const AppName = "sloprail"

// dataHome is the platform's directory for data a program keeps between runs.
//
// Deliberately outside the guarded project: state written into the tree would
// show up in the very diffs the engine reads, and in the user's git status.
func DataHome() (string, error) {
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

// EncodeWorkspace turns a working directory into one path component, the way
// Claude Code encodes one for its own transcripts.
//
// A 1:1 substitution rather than a hash: the same reasoning a10n records, that
// a directory a person can read is worth more when they are looking at their
// own state than the shorter name would be. Symlinks are resolved first because
// macOS reports /var where the filesystem holds /private/var, and the two would
// otherwise be two different sessions of the same tree.
func EncodeWorkspace(dir string) string {
	return nonAlnum.ReplaceAllString(WorkspaceAnchor(dir), "-")
}

// WorkspaceAnchor is the tree a directory belongs to: its git root where there
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
func WorkspaceAnchor(dir string) string {
	if root, err := gitrepo.Root(dir); err == nil && root != "" {
		return root
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// StateDB resolves where one session's state lives:
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
//     not before the anchor moved to the git root — see WorkspaceAnchor.
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
// WorkspaceAnchor's job, and it is the one thing taken here.
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
//
// What the parent MAY read across is only what is content-addressed, so that it is a
// statement about the very same bytes whichever folder ran it: a sub-agent's FINISHED,
// PASSING run of a rule that matches exactly (the rule's qualified name, the rule's
// definition hash, the judged head commit and, where it matters, the base). The root asks it
// so as not to judge again a commit a sub-agent already passed (services/sr-session
// results_family.go, ForeignPass). Writes stay per agent, nothing is pooled, and anything that
// does not match exactly (another rule version, another head, a refusal) is not shared.
func StateDB(cwd, sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("sloprail: no session id")
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
	root, err := DataHome()
	if err != nil {
		return "", err
	}
	if cwd == "" {
		// Reached from a person running the CLI by hand, where the process's own
		// directory IS the tree they mean. A hook never arrives here: the engine
		// always sets the variable, to the sentinel above when it has no answer.
		if cwd, err = os.Getwd(); err != nil {
			return "", fmt.Errorf("sloprail: locate working directory: %w", err)
		}
	}
	return filepath.Join(root, AppName, "sessions", EncodeWorkspace(cwd), sessionID, "state.db"), nil
}

// StableIdentity is stableID with the walk's Degraded flag kept: whether the id
// is the conversation's origin or the continuation root the walk fell back to.
// See transcript.Identity for the tradeoff, and noteDegradedIdentity for how
// it is surfaced.
//
// Remembered for the life of the process, keyed by the record and its size
// and modification time: one hook asks for its session's identity several
// times (the scope, the store, the baseline), and each ask is a walk across the
// project directory's transcripts. A hook process lives for one event, so the
// answer cannot go stale in a way that matters; the file's size and time are
// in the key so that a longer-lived caller (a test) that rewrites a record is
// not handed the old answer.
func StableIdentity(path, cwd string) (transcript.Identity, error) {
	if path == "" {
		return transcript.Identity{}, fmt.Errorf("sloprail: no transcript path — the record of this session is what its identity is read from")
	}
	dir := ProjectDirOf(path, cwd)
	key := identityKey(dir, path)
	if key != "" {
		if hit, ok := identities.Load(key); ok {
			return hit.(transcript.Identity), nil
		}
	}
	id, err := transcript.ResolveStableSessionID(dir, path)
	if err == nil && key != "" {
		identities.Store(key, id)
	}
	return id, err
}

// identities is StableIdentity's per-process memory.
var identities sync.Map

func identityKey(dir, path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d", dir, path, fi.Size(), fi.ModTime().UnixNano())
}

// ProjectDirOf is where the conversation's OTHER transcripts live, given the
// path of this one.
//
// Taken from the transcript's own location wherever that is knowable, because
// the transcript is already in the directory being looked for and a path is a
// fact where an encoded working directory is an assumption. A sub-agent's record
// is nested under <project>/<session>/subagents/, so the directory is reached by
// climbing back out of that nesting rather than by encoding a cwd that for an
// isolated sub-agent names somewhere else entirely.
//
// How FAR out is not a constant, which is why the climb is delegated rather than
// written here as two calls to filepath.Dir. Real data carries a second layout,
// <session>/subagents/workflows/wf_<id>/agent-<id>.jsonl, one a fixed two-level
// climb resolves to <session>/subagents — a directory holding no transcripts at
// all, so the conversation's history would read as empty. SessionDirOfSubagent
// finds the subagents component instead of counting to it; see its note.
//
// Falls back to encoding the working directory when the path is empty, which is
// the SessionStart case: the hook fires as the session begins and no record has
// been written yet.
func ProjectDirOf(path, cwd string) string {
	if path == "" {
		return transcript.ProjectDir(transcript.ConfigDir(), cwd)
	}
	// A sub-agent's record is nested under <session>/subagents/; the
	// conversation's other transcripts sit beside the session's own file, one
	// level above that directory.
	if sessionDir := transcript.SessionDirOfSubagent(path); sessionDir != "" {
		return filepath.Dir(sessionDir)
	}
	return filepath.Dir(path)
}

// ChecksDB is where a repository's check results are kept (checkcache.OpenFile):
//
//	{data home}/sloprail/checks/{root commit}/results.jsonl
//
// Keyed by the repository (its root commit, which every worktree and clone of it shares)
// and by nothing else: a check result is a fact about a rule, a subject and an input,
// whichever session, agent or worktree recorded it. A repository with no commit has no
// results.
func ChecksDB(repoRoot string) (string, error) {
	id, err := gitrepo.RootCommit(repoRoot)
	if err != nil {
		return "", fmt.Errorf("sloprail: no repository identity for %s: %w", repoRoot, err)
	}
	home, err := DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, AppName, "checks", id, "results.jsonl"), nil
}

// StateCwd is the directory a ROOT session's stores are keyed by: where its record
// says it began, never where the hook happens to stand.
//
// An agent that works in another worktree (`cd ../wt`) reports that worktree as its
// cwd from then on. Keyed by it, the verdicts, baseline, loop-breaker counters and
// context state would silently reset at the first hook after the `cd`, and the
// session would be split over two stores while its folder registry (which is keyed
// by the starting directory) stayed behind. So the start is the key.
//
// A SUB-AGENT keeps its own cwd (its record is nested under the dispatching
// session's directory, and its isolated worktree is its own tree): it is separated
// from the root by its own session id, not by this. An empty record, or one that
// names no starting directory, falls back to cwd.
func StateCwd(record, cwd string) string {
	if record == "" || transcript.SessionDirOfSubagent(record) != "" {
		return cwd
	}
	if start, err := transcript.StartCwd(record); err == nil && start != "" {
		return start
	}
	return cwd
}
