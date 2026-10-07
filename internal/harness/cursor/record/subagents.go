package record

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/sloprail/sloprail/internal/harness"
)

// Cursor writes a sub-agent's transcript as a conversation of its own beside the session's
// (agent-transcripts/<id>/<id>.jsonl) and names its parent nowhere: not in the file, not in
// a hook payload (subagentStart / subagentStop never fire in print mode), not in the
// layout. What ties the two is the dispatch itself: the sub-agent's first user message is,
// word for word, the prompt of the Task call in its dispatcher's transcript (recorded:
// harness-mocks cursor-mock runs/subagent-transcripts, foreground-subagent-result,
// nested-subagents, nested-subagents-depth). The stream's own Task result carries the
// sub-agent's id, but only the stream, which sloprail does not see.
//
// So a sub-agent's dispatcher is the one conversation of the project holding a Task call
// with exactly its prompt. Two conversations holding one (two sessions that dispatched the
// same words) leave the sub-agent unattributed: a parent is never guessed. And a
// conversation is a sub-agent only when its sessionStart never fired (KindRoot).
var _ harness.SubagentParenter = Transcripts{}

// convo is one conversation of a project as the link reads it.
type convo struct {
	path   string
	root   bool
	prompt string   // the first user message, the envelope removed
	tasks  []string // the prompts of its Task calls
}

// conversations reads the transcripts beside the one at path (every conversation of its
// project), keyed by path. A transcript that cannot be read is left out.
func conversations(path string) map[string]*convo {
	out := map[string]*convo{}
	base := filepath.Dir(filepath.Dir(path)) // agent-transcripts/
	if filepath.Base(base) != "agent-transcripts" {
		return out
	}
	ents, err := os.ReadDir(base)
	if err != nil {
		return out
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(base, e.Name(), e.Name()+".jsonl")
		if c := readConvo(p); c != nil {
			out[p] = c
		}
	}
	return out
}

func readConvo(path string) *convo {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	c := &convo{path: path, root: isRootConversation(Transcripts{}.ConversationID(path))}
	seenUser := false
	_ = eachLine(f, func(_ int, line []byte) error {
		var l struct {
			Role    string `json:"role"`
			Message struct {
				Content []struct {
					Type  string `json:"type"`
					Text  string `json:"text"`
					Name  string `json:"name"`
					Input struct {
						Prompt string `json:"prompt"`
					} `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &l) != nil {
			return nil
		}
		switch l.Role {
		case "user":
			if seenUser {
				return nil
			}
			seenUser = true
			var text string
			for _, b := range l.Message.Content {
				if b.Type == "text" {
					text += b.Text
				}
			}
			c.prompt = unwrapQuery(text)
		case "assistant":
			for _, b := range l.Message.Content {
				if b.Type == "tool_use" && b.Name == "Task" && b.Input.Prompt != "" {
					c.tasks = append(c.tasks, b.Input.Prompt)
				}
			}
		}
		return nil
	})
	return c
}

// isRootConversation reports whether sloprail's store holds the sessionStart marker of the
// conversation (KindRoot). Only the marker is looked for, not the store loaded.
func isRootConversation(conversationID string) bool {
	path, err := ToolResultsPath(conversationID)
	if err != nil {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	root := false
	_ = eachLine(f, func(_ int, line []byte) error {
		if root || !bytes.Contains(line, []byte(`"`+KindRoot+`"`)) {
			return nil
		}
		var l StoredLine
		root = json.Unmarshal(line, &l) == nil && l.Kind == KindRoot
		return nil
	})
	return root
}

// parentIn is the dispatcher of sub-agent s among the conversations: the one holding a Task
// call with its prompt, "" when there is none or more than one.
func parentIn(all map[string]*convo, s *convo) string {
	if s.root || s.prompt == "" {
		return ""
	}
	parent := ""
	for p, c := range all {
		if c == s {
			continue
		}
		for _, t := range c.tasks {
			if t != s.prompt {
				continue
			}
			if parent != "" && parent != p {
				return ""
			}
			parent = p
			break
		}
	}
	return parent
}

// ParentOf implements harness.SubagentParenter.
func (Transcripts) ParentOf(transcriptPath string) string {
	all := conversations(transcriptPath)
	s := all[transcriptPath]
	if s == nil {
		s = readConvo(transcriptPath)
		if s == nil {
			return ""
		}
	}
	return parentIn(all, s)
}

// SubagentFiles implements harness.SubagentLocator: the sub-agents the conversation at
// transcriptPath dispatched, and the ones those dispatched in turn, however deep.
func (Transcripts) SubagentFiles(transcriptPath string) []harness.SubagentFile {
	all := conversations(transcriptPath)
	parents := map[string]string{}
	for p, c := range all {
		parents[p] = parentIn(all, c)
	}
	want := map[string]bool{transcriptPath: true}
	var out []string
	for grew := true; grew; {
		grew = false
		for p, par := range parents {
			if par == "" || want[p] || !want[par] {
				continue
			}
			want[p] = true
			grew = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	files := make([]harness.SubagentFile, 0, len(out))
	for _, p := range out {
		files = append(files, harness.SubagentFile{Path: p, Rel: filepath.Base(p)})
	}
	return files
}
