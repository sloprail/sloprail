package transcript

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ResolveCitationAcrossSessions grounds one request in a project's sessions,
// starting from the current one.
//
// A commit is judged at a Stop that may be in a later session than the one that
// made it, so a quote in a commit's trailer may sit in a transcript other than
// the current session's. Lookup is: the current session first, then the
// project's other sessions from newest to oldest (by modification time; ties by
// name, so the order is the same every run).
//
// The first session that CONTAINS the quote is the one that answers, and there
// it must match exactly once, on the same rules as ResolveCitation: a quote in
// two entries of that session is ambiguous, and an ambiguity is not resolved by
// looking further back. A quote found in no session is a ResolutionError.
//
// A transcript that cannot be read does not stop the search — one broken file
// elsewhere in the directory must not disable grounding — but it is not
// silence either: when nothing resolves, the error names what could not be
// read, so a quote that was missed for that reason says so.
//
// current may be empty (no session known); projectDir may be empty (no other
// sessions to search).
func ResolveCitationAcrossSessions(current, projectDir string, req CitationRequest) (Citation, error) {
	if req.Quote == "" {
		return Citation{}, fmt.Errorf("an empty quote grounds nothing")
	}
	candidates, err := sessionCandidates(current, projectDir)
	if err != nil {
		return Citation{}, err
	}
	var unreadable []string
	for _, path := range candidates {
		matches, err := CiteInSession(path, req.Quote, req.SourceTypes)
		if err != nil {
			if errors.Is(err, ErrNoSessionRoot) {
				continue
			}
			unreadable = append(unreadable, fmt.Sprintf("%s (%v)", path, err))
			continue
		}
		if len(matches) == 0 {
			continue
		}
		return ResolveCitation(path, req)
	}
	msg := fmt.Sprintf("citation %s does not resolve in any session of this project: the quote is not there word for word", req)
	// The words may be there but in text the pool leaves out (sloprail's own
	// output, a sub-agent's reply, ...): say which, or the caller hunts for a
	// typo in a quote that is verbatim.
	if current != "" {
		if hint := UnresolvedHint(current, req.Quote, req.SourceTypes); hint != "" {
			msg = fmt.Sprintf("citation %s does not resolve in any session of this project. %s", req, hint)
		}
	}
	if len(unreadable) > 0 {
		msg += "; could not read: " + strings.Join(unreadable, ", ")
	}
	return Citation{}, &ResolutionError{Msg: msg}
}

// sessionCandidates is the transcripts to search, in order: current, then the
// rest of projectDir's top-level transcripts, newest first.
func sessionCandidates(current, projectDir string) ([]string, error) {
	var out []string
	if current != "" {
		out = append(out, current)
	}
	if projectDir == "" {
		return out, nil
	}
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("transcript: list sessions in %s: %w", projectDir, err)
	}
	type dated struct {
		path string
		mod  int64
	}
	var others []dated
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(projectDir, e.Name())
		if current != "" && sameFile(path, current) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // vanished between the listing and the stat
		}
		others = append(others, dated{path, info.ModTime().UnixNano()})
	}
	sort.Slice(others, func(i, j int) bool {
		if others[i].mod != others[j].mod {
			return others[i].mod > others[j].mod
		}
		return others[i].path < others[j].path
	})
	for _, o := range others {
		out = append(out, o.path)
	}
	return out, nil
}
