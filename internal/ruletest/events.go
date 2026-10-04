package ruletest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/cyclemod"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/tagmod"
	"github.com/sloprail/sloprail/internal/tooluse"
)

// maxHeadRead bounds the bytes read from a revision for a default.
const maxHeadRead = 8 << 20

// BuildEvent turns a trajectory step's event fields into the engine's event, the
// same value the module for that kind would have produced from a harness payload.
//
// The author writes the facts only the author knows (`path`, `newContent`,
// `command`, `tags`); what the engine derives is derived here exactly as the
// modules derive it: the `sr:` markers of the text, the parsed `invocations` of a
// command line, `resultKnown`, the settled bytes of a Post file event, a Pre
// update's old bytes off the disk. Anything the author writes explicitly wins, so
// `resultKnown: false` models the write the engine could not compute.
//
// A field the kind does not declare is an error, never an ignored key: a mistyped
// field is a case that asserts less than its author believes.
func BuildEvent(reg *module.Registry, kind string, fields map[string]any, repo string) (event.Event, error) {
	if kind == "PreFileWrite" || kind == "PostFileWrite" {
		pre := strings.HasPrefix(kind, "Pre")
		path, _ := fields["path"].(string)
		_, err := os.Stat(filepath.Join(repo, path))
		switch {
		case pre && err == nil:
			kind = filemod.KindPreUpdate
		case pre:
			kind = filemod.KindPreCreate
		case err == nil && existedAtHead(repo, path):
			kind = filemod.KindPostUpdate
		default:
			kind = filemod.KindPostCreate
		}
	}
	decl, ok := reg.KindDeclFor(kind)
	if !ok {
		return event.Event{}, fmt.Errorf("unknown event kind %q; this build produces: %s", kind, strings.Join(reg.DeclaredKinds(), ", "))
	}
	allowed := map[string]bool{}
	for _, f := range decl.Fields {
		allowed[f.Name] = true
	}
	if kind == commandmod.KindPreInvoke {
		allowed["command"] = true
	}
	for _, k := range keys(fields) {
		if !allowed[k] {
			names := make([]string, 0, len(allowed))
			for n := range allowed {
				names = append(names, n)
			}
			sort.Strings(names)
			return event.Event{}, fmt.Errorf("%s has no field %q (it carries: %s)", kind, k, strings.Join(names, ", "))
		}
	}

	switch kind {
	case filemod.KindPreCreate, filemod.KindPreUpdate, filemod.KindPreDelete,
		filemod.KindPostCreate, filemod.KindPostUpdate, filemod.KindPostDelete:
		return buildFileEvent(kind, fields, repo)
	case commandmod.KindPreInvoke:
		raw, _ := fields["raw"].(string)
		if c, ok := fields["command"].(string); ok {
			if raw != "" && raw != c {
				return event.Event{}, fmt.Errorf("%s: `command` and `raw` are the same field and differ", kind)
			}
			raw = c
		}
		if strings.TrimSpace(raw) == "" {
			return event.Event{}, fmt.Errorf("%s needs `command:` (the shell command line)", kind)
		}
		ev := commandmod.ExtractCommand(raw).Event()
		if c, ok := fields["citations"]; ok {
			ev.Fields["citations"] = c
		}
		return ev, nil
	case tooluse.KindPreToolUse:
		tool, _ := fields["tool"].(string)
		if tool == "" {
			return event.Event{}, fmt.Errorf("%s needs `tool:` (the tool's name)", kind)
		}
		input, _ := fields["input"].(map[string]any)
		return tooluse.ToolEvent{Tool: tool, Input: input}.Event(), nil
	case tagmod.KindPostTagWrite:
		return buildTagEvent(fields)
	case cyclemod.KindStop:
		return cyclemod.Event(), nil
	}
	// A kind a future module declares: its declared fields, as written.
	out := map[string]any{}
	for _, f := range decl.Fields {
		if v, ok := fields[f.Name]; ok {
			out[f.Name] = v
			continue
		}
		out[f.Name] = zeroOf(f.Type)
	}
	return event.Event{Kind: kind, Fields: out}, nil
}

func zeroOf(t module.FieldType) any {
	switch t {
	case module.TypeString:
		return ""
	case module.TypeBool:
		return false
	case module.TypeInt:
		return 0
	case module.TypeList:
		return []any{}
	case module.TypeMap:
		return map[string]any{}
	}
	return nil
}

func buildTagEvent(fields map[string]any) (event.Event, error) {
	var t tagmod.TagEvent
	list, _ := fields["tags"].([]any)
	for _, e := range list {
		switch v := e.(type) {
		case string:
			t.Tags = append(t.Tags, tagmod.Tag{Label: strings.TrimPrefix(v, "#")})
		case map[string]any:
			label, _ := v["label"].(string)
			seen, _ := v["seen"].(bool)
			if label == "" {
				return event.Event{}, fmt.Errorf("PostTagWrite: a tag needs a label")
			}
			t.Tags = append(t.Tags, tagmod.Tag{Label: strings.TrimPrefix(label, "#"), Seen: seen})
		default:
			return event.Event{}, fmt.Errorf("PostTagWrite: tags are strings (`research`) or {label, seen}")
		}
	}
	return t.Event(), nil
}

func buildFileEvent(kind string, fields map[string]any, repo string) (event.Event, error) {
	path, _ := fields["path"].(string)
	if path == "" {
		return event.Event{}, fmt.Errorf("%s needs `path:` (repository-relative)", kind)
	}
	var f filemod.FileEvent
	f.Path = path
	str := func(k string) (string, bool) {
		v, ok := fields[k]
		if !ok {
			return "", false
		}
		s, _ := v.(string)
		return s, true
	}
	flag := func(k string, def bool) bool {
		if v, ok := fields[k].(bool); ok {
			return v
		}
		return def
	}
	disk := func() (string, bool) {
		b, err := os.ReadFile(filepath.Join(repo, path))
		return string(b), err == nil
	}
	head := func() (string, bool) {
		return gitrepo.ContentAtWithin(repo, "HEAD", "./"+path, maxHeadRead)
	}

	pre := strings.HasPrefix(kind, "Pre")
	hasNew := kind != filemod.KindPreDelete && kind != filemod.KindPostDelete
	hasOld := kind != filemod.KindPreCreate && kind != filemod.KindPostCreate

	if hasNew {
		nc, given := str("newContent")
		switch {
		case given:
			f.NewContent = nc
		case pre && flag("resultKnown", true):
			return event.Event{}, fmt.Errorf("%s of %s needs `newContent:` (the bytes the write leaves behind), or `resultKnown: false` for a write the engine could not compute", kind, path)
		case pre:
			// resultKnown: false, no bytes: the engine's own "could not compute".
		default:
			s, ok := disk()
			if !ok {
				return event.Event{}, fmt.Errorf("%s of %s: the file is not in the repository (a Post event describes a settled file; create it with a `run:` step, or give `newContent:`)", kind, path)
			}
			f.NewContent = s
		}
		if pre {
			f.ResultKnown = flag("resultKnown", given)
		}
		f.NewContentKnown = flag("newContentKnown", true)
		f.NewMarkers = filemod.Scan(f.NewContent)
	}
	if hasOld {
		oc, given := str("oldContent")
		switch {
		case given:
			f.OldContent = oc
		case pre:
			s, ok := disk()
			if !ok {
				return event.Event{}, fmt.Errorf("%s of %s: the file is not in the repository (use PreFileCreate for a new file, or give `oldContent:`)", kind, path)
			}
			f.OldContent = s
		default:
			s, ok := head()
			if !ok {
				return event.Event{}, fmt.Errorf("%s of %s: the file is not at HEAD to read its old bytes from (give `oldContent:`)", kind, path)
			}
			f.OldContent = s
		}
		f.OldContentKnown = flag("oldContentKnown", true)
		f.OldMarkers = filemod.Scan(f.OldContent)
	}
	f.Seen = flag("seen", false)

	ev := f.Event(kind)
	for _, k := range []string{"newMarkers", "oldMarkers"} {
		if v, ok := fields[k]; ok {
			list, ok := v.([]any)
			if !ok {
				return event.Event{}, fmt.Errorf("%s: %s is a list of {kind, fqn, line}", kind, k)
			}
			ev.Fields[k] = list
		}
	}
	if c, ok := fields["citations"]; ok {
		ev.Fields["citations"] = c
	}
	return ev, nil
}

func existedAtHead(repo, path string) bool {
	_, ok := gitrepo.ContentAtWithin(repo, "HEAD", "./"+path, maxHeadRead)
	return ok
}
