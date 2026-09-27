package declaration

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/sloprail/sloprail/internal/transcript"
)

// CitationPrerequisite is `require: [{citation: ...}]`: the action must carry
// at least one citation that resolved in one of the named pools.
//
// A citation travels on the ACTION, never in the content: an `sr-file
// write|edit|delete ... --cite:<pool> '<quote>'` invocation grounds the file it
// changes, and a `sr-session trajectory cite '<quote>' && <command>` chain
// grounds everything the command does. sloprail resolves each quote against the
// session's own record before the rule sees it, so this checks EXISTENCE — a
// real entry in the named pool. Whether the quote actually grounds the change
// is for the rule's judge, which reads `event.citations`.
//
// Written `citation: true` for the default (the user's own words), or
// `citation: {source_types: [user, tool_result]}` to accept other pools.
type CitationPrerequisite struct {
	// SourceTypes are the pools a citation may have resolved in — cite's
	// --source-types vocabulary. Empty means user alone.
	SourceTypes []string `yaml:"source_types"`
}

// Pools is SourceTypes with the default applied. Names are validated at load,
// so an unknown one never reaches here from a loaded rule.
func (c CitationPrerequisite) Pools() []transcript.SourceType {
	if len(c.SourceTypes) == 0 {
		return []transcript.SourceType{transcript.SourceUser}
	}
	out := make([]transcript.SourceType, 0, len(c.SourceTypes))
	for _, name := range c.SourceTypes {
		if s, ok := transcript.ParseSourceType(name); ok {
			out = append(out, s)
		}
	}
	return out
}

// UnmarshalYAML accepts `true` or a mapping with only `source_types`. `false`
// is refused rather than read as "no requirement" — an entry that requires
// nothing is a mistake, not a setting — and an unknown key is refused rather
// than ignored, so `citation: {sourceTypes: [tool_result]}` cannot quietly
// mean the user pool.
func (c *CitationPrerequisite) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		var on bool
		if err := n.Decode(&on); err != nil {
			return fmt.Errorf("citation: want true or {source_types: [...]}, got %q", n.Value)
		}
		if !on {
			return fmt.Errorf("citation: false requires nothing — remove the entry instead")
		}
		*c = CitationPrerequisite{}
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("citation: want true or {source_types: [...]}")
	}
	for i := 0; i < len(n.Content); i += 2 {
		if k := n.Content[i].Value; k != "source_types" {
			return fmt.Errorf("citation: unknown key %q — the one key is source_types", k)
		}
	}
	type plain CitationPrerequisite
	var p plain
	if err := n.Decode(&p); err != nil {
		return fmt.Errorf("citation: %w", err)
	}
	*c = CitationPrerequisite(p)
	return nil
}

// citationKinds are the event kinds that carry `citations`: every file kind
// and a command invocation. A rule waking on any other kind could never see a
// citation, so requiring one there would refuse every time.
var citationKinds = map[string]bool{
	KindPreFileCreate:    true,
	KindPreFileUpdate:    true,
	KindPreFileDelete:    true,
	KindPostFileCreate:   true,
	KindPostFileUpdate:   true,
	KindPostFileDelete:   true,
	KindPreCommandInvoke: true,
}
