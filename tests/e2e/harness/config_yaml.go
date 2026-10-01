package harness

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// mergeDisabled adds names to the `disabled:` list of a project's .sloprail/config.yaml
// body and returns the new body. The YAML is parsed and rewritten as a document, not
// appended to as text: `disabled:` need not be the last key (a line appended at the end
// would land under another key), the file need not end in a newline, and an entry already
// there is not repeated. Every other key and comment is kept.
func mergeDisabled(body string, names []string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	if doc.Kind == 0 { // an empty file
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", fmt.Errorf("the config's top level is not a mapping")
	}
	var list *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "disabled" {
			list = root.Content[i+1]
			break
		}
	}
	if list == nil {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "disabled"},
			&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"})
		list = root.Content[len(root.Content)-1]
	}
	if list.Kind == yaml.ScalarNode && list.Tag == "!!null" { // `disabled:` with nothing after it
		*list = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}
	if list.Kind != yaml.SequenceNode {
		return "", fmt.Errorf("`disabled:` is not a list")
	}
	have := map[string]bool{}
	for _, n := range list.Content {
		have[n.Value] = true
	}
	for _, name := range names {
		if !have[name] {
			list.Content = append(list.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name})
			have[name] = true
		}
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", fmt.Errorf("encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("encode: %w", err)
	}
	return out.String(), nil
}
