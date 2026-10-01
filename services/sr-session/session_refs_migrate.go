package main

import (
	"os"
	"strings"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// refsSchemaVersion is the version of what a session's recorded refs keep beside the rows:
// the pin of each owed tip, the start each ref was cut at, the home of each worktree folder
// (session_refs_keep.go). A store written before that has rows and none of it.
const refsSchemaVersion = "2"

// migrateRefs brings a session store written by an earlier engine up to refsSchemaVersion,
// the first hook that opens it. It is idempotent (a store at the current version costs one
// read), changes no row's tip and settles nothing: it only adds what the rows need to stay
// judgeable.
//   - every recorded tip whose commit still exists is pinned, so a branch deleted from now on
//     does not lose its commits to a garbage collection;
//   - every ref gets its start: from its reflog while the ref lives, else the merge base of
//     the first tip it was recorded at with the folder's HEAD (where it was cut off);
//   - every worktree folder gets its home, so the folder's removal is survived.
//
// A tip already collected before this ran cannot be recovered; it stays as it was.
func migrateRefs(reg sessionstate.Store, sessionID string) {
	if v, had, err := reg.Meta(sessionstate.MetaRefsSchema); err != nil || (had && v == refsSchemaVersion) {
		return
	}
	folders, err := reg.Folders(sessionID)
	if err != nil {
		return
	}
	for _, f := range folders {
		if f.Role == sessionstate.FolderAdHoc {
			if _, err := os.Stat(f.Path); err == nil {
				noteFolderHome(reg, f.Path)
			}
		}
	}
	rows, err := reg.Refs(sessionID, "")
	if err != nil {
		return
	}
	for _, r := range rows {
		dir := r.Folder
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			home, had, _ := reg.Meta(folderHomeKey(dir))
			if !had {
				continue
			}
			dir = home
		}
		if held, err := gitrepo.RefTip(dir, r.Tip); err != nil || held == "" {
			continue
		}
		pinTip(dir, sessionID, r.Folder, r.Name, r.Tip)
		if refStart(reg, dir, r.Folder, r.Name, r.Tip) == "" && strings.HasPrefix(r.Name, "refs/") && r.FirstTip != "" {
			if mb, err := gitrepo.MergeBaseOf(dir, r.FirstTip, "HEAD"); err == nil && mb != "" {
				_ = reg.SetMeta(refStartKey(r.Folder, r.Name), mb)
			}
		}
	}
	_ = reg.SetMeta(sessionstate.MetaRefsSchema, refsSchemaVersion)
}
