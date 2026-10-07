package declaration

import (
	"strings"
	"testing"
)

// sr:proves loading/retired-file-guard-keys-are-refused
func TestValidateFileGuard_RefusesSessionRequireNamingTheGate(t *testing.T) {
	cases := map[string]Prerequisite{
		"skill":   {Skill: "authoring-guardrails", Files: []string{"gate.md"}},
		"context": {Context: "refactoring"},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			g := FileGuard{Match: `path startsWith "docs/"`, Require: []Prerequisite{req}}
			var found *Problem
			for _, p := range ValidateFileGuard(g, Env{Contexts: map[string]bool{"refactoring": true}}) {
				if p.Where == "require" {
					p := p
					found = &p
				}
			}
			if found == nil {
				t.Fatal("a file-guard with a session-dependent require loaded")
			}
			for _, want := range []string{"gate/<name>/gate.yaml", "event: PreFileWrite", "event.path", "require:"} {
				if !strings.Contains(found.Detail, want) {
					t.Errorf("message lacks %q:\n%s", want, found.Detail)
				}
			}
		})
	}
}

func TestValidateFileGuard_CitationRequireStaysLegal(t *testing.T) {
	g := FileGuard{Match: "**/*.md", Require: []Prerequisite{{Citation: &CitationPrerequisite{SourceTypes: []string{"user"}}}}}
	for _, p := range ValidateFileGuard(g, Env{}) {
		t.Errorf("a citation require on a file-guard was refused: %s", p.Detail)
	}
}
