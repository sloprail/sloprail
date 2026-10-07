package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sloprail/sloprail/internal/harness"
)

// Facts ABOUT a trajectory, not what is in it. `normalize` answers "what
// happened"; this answers "what IS this file" — whether it belongs to a
// sub-agent, which trajectory spawned it, and which it spawned. All three are
// settled by the path and the session's own records (the sub-agent directory
// beside the session, and the meta file a harness writes next to each sub-agent
// record), not by walking the entries — which is why they live here rather than
// as a filter over the entry stream.

// metaSuffix is what a harness appends to a sub-agent's record filename to name
// the companion file describing it: agent-<id>.jsonl has agent-<id>.meta.json
// beside it.
const metaSuffix = ".meta.json"

// jsonlSuffix is the record-file extension, named once so the two places that
// swap it for metaSuffix or trim it off a session directory agree.
const jsonlSuffix = ".jsonl"

// SubagentMeta is the companion record a harness writes beside a sub-agent's
// transcript, describing the delegation that created it.
//
// Observed, not promised: Claude Code writes agent-<id>.meta.json holding
// {agentType, description, toolUseId, spawnDepth}. Only ToolUseID is load-bearing
// here — it is the id of the tool_use in the PARENT trajectory that dispatched
// this sub-agent, and so the one thread back to the immediate parent. The rest
// is carried because a reader asking "what kind of sub-agent" or "how deep" is
// asking a fair question the file already answers, and dropping the fields would
// make this a parser of one field rather than of the record.
type SubagentMeta struct {
	// AgentType is what the harness dispatched — the sub-agent's role
	// ("Explore", "general-purpose"). Empty when the harness did not record one.
	AgentType string `json:"agentType"`

	// Description is the one-line summary of the delegated task.
	Description string `json:"description"`

	// ToolUseID is the id of the tool_use block that dispatched this sub-agent,
	// carried in the PARENT trajectory. It is how the immediate parent is found:
	// the parent is whichever trajectory in the session holds a tool_use with
	// this id. Empty when the harness did not record it — in which case the
	// parent cannot be derived from the meta file and ParentPath says so rather
	// than guessing.
	ToolUseID string `json:"toolUseId"`

	// SpawnDepth is how many delegations deep this sub-agent sits — 1 for one
	// dispatched from the root, greater when a sub-agent dispatched it in turn.
	// Kept so a reader can tell a top-level sub-agent from a nested one without
	// walking the chain; not relied on for correlation, which the id settles.
	SpawnDepth int `json:"spawnDepth"`
}

// metaPathOf is the companion meta file for a sub-agent transcript path:
// agent-<id>.jsonl -> agent-<id>.meta.json. Returns "" for a path that is not a
// .jsonl record, since only a record file has a meta companion.
func metaPathOf(transcriptPath string) string {
	if !strings.HasSuffix(transcriptPath, jsonlSuffix) {
		return ""
	}
	return strings.TrimSuffix(transcriptPath, jsonlSuffix) + metaSuffix
}

// ReadSubagentMeta reads the companion meta file for a sub-agent's transcript.
//
// The boolean reports whether a meta file was there at all, kept apart from the
// error so the ordinary case — a ROOT trajectory, which has no meta file — is
// not an error to handle but a plain "no". A meta file that exists but will not
// parse IS an error: the harness wrote something and it was not what we read, a
// difference worth surfacing rather than swallowing into "no meta".
func ReadSubagentMeta(transcriptPath string) (SubagentMeta, bool, error) {
	metaPath := metaPathOf(transcriptPath)
	if metaPath == "" {
		return SubagentMeta{}, false, nil
	}
	b, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return SubagentMeta{}, false, nil
		}
		return SubagentMeta{}, false, fmt.Errorf("transcript: read sub-agent meta %s: %w", metaPath, err)
	}
	var m SubagentMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return SubagentMeta{}, false, fmt.Errorf("transcript: parse sub-agent meta %s: %w", metaPath, err)
	}
	return m, true, nil
}

// IsSubagentTranscript reports whether the trajectory at path belongs to a
// sub-agent rather than the main line.
//
// Two independent signals, either of which settles it, because they come from
// different harnesses and a rule must not depend on both being present:
//
//   - A meta file beside the record. Claude Code writes agent-<id>.meta.json for
//     every sub-agent it dispatches to its own file, so the companion's presence
//     is a positive mark of a sub-agent transcript.
//   - isSidechain on the record's own entries. This is the ground truth the spec
//     names: every sub-agent record carries it, in every layout measured. It is
//     what answers the question when a meta file is absent — and it is what a
//     harness that inlines sub-agent work has instead of a separate file at all.
//
// The meta file is checked first because it is a single stat rather than a read
// of the record, and is decisive when present. The entry scan is the fallback,
// and it stops at the first sidechain record rather than reading the whole file.
//
// An unreadable record answers false rather than erroring: a file we cannot open
// is a broken environment for whoever reads it properly, and this predicate's job
// is only to classify what it can see. A path under a subagents/ directory is NOT
// used as a signal on its own — the directory says where a harness put the file,
// which is a weaker fact than what the file says about itself.
func IsSubagentTranscript(path string) bool {
	if _, ok, err := ReadSubagentMeta(path); ok && err == nil {
		return true
	}
	sidechain := false
	_ = scanFile(path, func(rec harness.Record) bool {
		if rec.UUID == "" {
			return true
		}
		if rec.IsSidechain {
			sidechain = true
		}
		return false // the first uuid-carrying record settles it
	})
	return sidechain
}

// SubagentPaths returns the paths of the sub-agent transcripts spawned off the
// trajectory at path — the separate-file harnesses' "and some of it lives over
// there".
//
// The sub-agent records of a session sit in <session>/subagents/ beside the
// session's own transcript, so for a root trajectory at <dir>/<session>.jsonl the
// directory to enumerate is <dir>/<session>/subagents/. For a sub-agent
// trajectory the same construction names ITS own subagents directory, which is
// how a nested delegation (a sub-agent that dispatched another) is found — the
// derivation is uniform, the file's own location deciding whose children it
// enumerates.
//
// Empty, not an error, when there is no subagents directory: a trajectory that
// spawned nothing has none, which is the common case and a plain "none" rather
// than a fault. Only agent-<id>.jsonl files are returned — the meta companions
// and anything else a harness leaves there are not transcripts. The result is
// sorted, so the enumeration is stable for a caller that prints or compares it.
//
// A harness that INLINES sub-agent work writes no such directory and this is
// empty for it; that is correct, and the spec's note that such work is found via
// .isSidechain on the entries rather than here is the other half of the same
// fact.
func SubagentPaths(path string) ([]string, error) {
	dir := subagentDirOf(path)
	if dir == "" {
		return nil, nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("transcript: enumerate sub-agents in %s: %w", dir, err)
	}
	var paths []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, subagentFilePrefix) || !strings.HasSuffix(name, jsonlSuffix) {
			continue
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	sort.Strings(paths)
	return paths, nil
}

// isSubagentRecord reports whether the record at path is a sub-agent's: by
// what it says about itself (IsSubagentTranscript), OR by where the harness
// filed it — an agent-<id>.jsonl under a subagents directory. Either suffices.
// A question deciding whose words a record's "user" messages are must not
// read a sub-agent's record as a root because its meta.json is missing or its
// first record is not written yet: that would make the parent's dispatch
// prompt citable as the end user's words.
func isSubagentRecord(path string) bool {
	if p, ok := harness.ForTranscript(path).Transcripts().(harness.SubagentLocator); ok {
		if _, sub := p.ParentRecord(path); sub {
			return true
		}
	}
	name := filepath.Base(path)
	if strings.HasPrefix(name, subagentFilePrefix) && strings.HasSuffix(name, jsonlSuffix) && SessionDirOfSubagent(path) != "" {
		return true
	}
	return IsSubagentTranscript(path)
}

// IsSubagentRecord is isSubagentRecord for callers outside the package: whether the
// record at path is a sub-agent's, by what it says of itself or by where, or how, the
// harness filed it (harness.SubagentLocator).
func IsSubagentRecord(path string) bool { return isSubagentRecord(path) }

// SessionRootOf returns the ROOT record of the session the trajectory at path
// belongs to — the end user's own conversation — climbing out of however many
// levels of delegation path sits under. A root answers itself.
//
// A sub-agent's record is nested under the directory named for the record that
// dispatched it (SessionDirOfSubagent), so its dispatcher's record is that
// directory plus .jsonl; the climb repeats while the record reached is still a
// sub-agent's, so a nested delegation reaches the root too.
//
// Returns "" when path is a sub-agent's record whose root is not on disk where
// the layout puts it: an orphaned record is not a session this can name the
// root of, and guessing one is how one conversation's words are cited as
// another's.
func SessionRootOf(path string) string {
	cur := path
	for isSubagentRecord(cur) {
		if p, ok := harness.ForTranscript(cur).Transcripts().(harness.SubagentLocator); ok {
			if parent, sub := p.ParentRecord(cur); sub {
				if parent == "" || parent == cur {
					return ""
				}
				cur = parent
				continue
			}
		}
		dir := SessionDirOfSubagent(cur)
		if dir == "" {
			return ""
		}
		next := dir + jsonlSuffix
		if fi, err := os.Stat(next); err != nil || fi.IsDir() || next == cur {
			return ""
		}
		cur = next
	}
	return cur
}

// DescendantSubagentPaths returns every sub-agent record beneath the trajectory
// at path — the ones it dispatched, the ones THOSE dispatched, and records a
// harness nests deeper still (subagents/workflows/wf_<id>/, see
// SessionDirOfSubagent) — in lexical order. SubagentPaths lists one level; this
// is the whole tree, which is what "the session's own work" spans.
//
// Empty, not an error, when path dispatched nothing. A subtree that cannot be
// read is an error: a caller deciding that a quote lands on exactly one entry
// must not decide it over records it silently skipped.
func DescendantSubagentPaths(path string) ([]string, error) {
	if l, ok := harness.ForTranscript(path).Transcripts().(harness.SubagentLocator); ok {
		var paths []string
		for _, f := range l.SubagentFiles(path) {
			if strings.HasSuffix(f.Path, jsonlSuffix) {
				paths = append(paths, f.Path)
			}
		}
		if len(paths) > 0 {
			sort.Strings(paths)
			return paths, nil
		}
		// None found by the harness that owns path: a record in another harness's layout
		// (Claude Code's, which the harness has no way to claim) is still read by its own.
	}
	dir := subagentDirOf(path)
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if !d.IsDir() && strings.HasPrefix(name, subagentFilePrefix) && strings.HasSuffix(name, jsonlSuffix) {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("transcript: enumerate sub-agents under %s: %w", dir, err)
	}
	return paths, nil
}

// subagentDirOf is the directory a trajectory's own sub-agent records sit in:
// <session>/subagents for a record at <session>.jsonl. Built by the same
// construction SubagentTranscriptPath uses, so a root and a sub-agent both name
// the directory of the children THEY dispatched.
func subagentDirOf(path string) string {
	if !strings.HasSuffix(path, jsonlSuffix) {
		return ""
	}
	base := strings.TrimSuffix(path, jsonlSuffix)
	return filepath.Join(base, SubagentDir)
}

// ParentPath returns the path of the trajectory that dispatched the sub-agent at
// path — its IMMEDIATE parent, which may itself be a sub-agent — or "" when this
// is a root trajectory or its parent cannot be derived.
//
// The link is not written down as a path anywhere; it is DERIVED. A sub-agent's
// meta file carries the toolUseId of the tool_use that dispatched it, and that
// tool_use lives in the parent's trajectory. So the parent is found by reading
// this sub-agent's toolUseId and then searching the session's trajectories for
// the one holding a tool_use with that id.
//
// searchDir is where the session's other trajectories live — the directory
// holding the root transcript and the subagents/ tree. The immediate parent may
// be the root (at <searchDir>/<session>.jsonl) or another sub-agent (under
// <searchDir>/.../subagents/), so both are searched: every .jsonl at the top of
// searchDir and every agent-<id>.jsonl beneath any subagents/ directory in it.
//
// Returns "" without error in the ordinary "no parent to name" cases — a root
// trajectory has no meta file, and a meta file may carry an empty toolUseId (a
// harness that does not record it, the mock among them) which cannot be
// correlated. A meta file present but naming a toolUseId no trajectory holds is
// different: the dispatching call is somewhere the search could not see, so this
// returns "" and leaves the caller to report absence rather than inventing a
// link. The one hard error is an unreadable search directory, which is a broken
// environment rather than a trajectory without a parent.
func ParentPath(path, searchDir string) (string, error) {
	if l, ok := harness.ForTranscript(path).Transcripts().(harness.SubagentLocator); ok {
		if parent, sub := l.ParentRecord(path); sub {
			return parent, nil
		}
	}
	meta, ok, err := ReadSubagentMeta(path)
	if err != nil {
		return "", err
	}
	if !ok || meta.ToolUseID == "" {
		// No meta file (a root), or one that did not record the dispatching id:
		// nothing to correlate, and no parent to name.
		return "", nil
	}
	if searchDir == "" {
		return "", nil
	}
	return trajectoryContainingToolUse(searchDir, meta.ToolUseID, path)
}

// trajectoryContainingToolUse finds the trajectory in searchDir (or its
// subagents/ subtree) that holds a tool_use block with id toolUseID, skipping
// the file self so a sub-agent is not reported as its own parent.
//
// The search is over the session's own files rather than the whole disk: the
// top-level .jsonl transcripts in searchDir (the root, and any sibling records),
// and the agent-<id>.jsonl files under every subagents/ directory within it. A
// tool_use id is unique within a session, so the first file carrying it is the
// answer.
//
// An unreadable individual transcript is skipped rather than fatal — another
// session's half-written file, or one this walk has no business failing over, is
// not this correlation's problem, exactly as the sibling walk treats one. Only
// the top-level directory read failing is an error, since that is the directory
// the whole search is over.
func trajectoryContainingToolUse(searchDir, toolUseID, self string) (string, error) {
	var found string
	err := walkSessionTranscripts(searchDir, func(p string) bool {
		if sameFile(p, self) {
			return true
		}
		if transcriptHasToolUse(p, toolUseID) {
			found = p
			return false
		}
		return true
	})
	if err != nil {
		return "", err
	}
	return found, nil
}

// walkSessionTranscripts calls visit for each transcript belonging to the
// session rooted at dir: the top-level .jsonl files, and the agent-<id>.jsonl
// records under any subagents/ directory reachable from it. Returning false from
// visit stops the walk.
//
// Only the top-level read is allowed to fail the walk; a subagents/ directory
// that cannot be read is skipped, on the same reasoning an unreadable sibling
// transcript is skipped — the walk answers what it can see.
func walkSessionTranscripts(dir string, visit func(path string) bool) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("transcript: read %s: %w", dir, err)
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), jsonlSuffix) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		// A top-level transcript names a session; its own sub-agents sit under
		// <session>/subagents. Visit the transcript, then descend into whatever
		// it dispatched.
		if !visit(p) {
			return nil
		}
	}
	// Descend into each session directory's subagents tree. These are the
	// directories named like a session (a top-level <session>.jsonl has a sibling
	// <session>/ directory), but rather than pair them we simply look for any
	// subagents/ directory one level down, which is where sub-agent records sit.
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		subDir := filepath.Join(dir, e.Name(), SubagentDir)
		if !walkSubagentDir(subDir, visit) {
			return nil
		}
	}
	return nil
}

// walkSubagentDir visits every agent-<id>.jsonl record directly inside a
// subagents/ directory, and recurses into each sub-agent's own subagents/ tree
// so a nested delegation is reached. Returns false when visit asked to stop.
//
// An unreadable or absent directory is simply nothing to visit — see
// walkSessionTranscripts on why a missing subagents tree is not a fault.
func walkSubagentDir(dir string, visit func(path string) bool) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, subagentFilePrefix) || !strings.HasSuffix(name, jsonlSuffix) {
			continue
		}
		if !visit(filepath.Join(dir, name)) {
			return false
		}
	}
	// A sub-agent may have dispatched its own sub-agents, nested under its own
	// <agent-id>/subagents directory beside its record.
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		nested := filepath.Join(dir, e.Name(), SubagentDir)
		if !walkSubagentDir(nested, visit) {
			return false
		}
	}
	return true
}

// transcriptHasToolUse reports whether any entry in the transcript at path holds
// a tool_use block with id toolUseID. An unreadable file answers false; see
// trajectoryContainingToolUse on why one transcript failing is not the walk's
// concern.
func transcriptHasToolUse(path, toolUseID string) bool {
	found := false
	_ = scanFile(path, func(rec harness.Record) bool {
		if rec.UUID == "" {
			return true
		}
		for _, id := range toolUseIDs(rec.Entry()) {
			if id == toolUseID {
				found = true
				return false
			}
		}
		return true
	})
	return found
}
