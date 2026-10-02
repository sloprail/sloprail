package declaration

import (
	"strings"
	"testing"
)

func TestValidateFileGuard_RefusesSessionRequireNamingTheGate(t *testing.T) {
	cases := map[string]Prerequisite{
		"skill":   {Skill: "authoring-guardrails", Files: []string{"gate.md"}},
		"context": {Context: "refactoring"},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			g := FileGuard{Match: `path startsWith "docs/"`, Dir: "/p/.sloprail/file-guard/docs-rule", Require: []Prerequisite{req}}
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
			for _, want := range []string{"gate/docs-rule-requires/gate.yaml", "event: PreFileWrite", `event.path startsWith "docs/"`, "require:"} {
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

func TestGateMatchFromFileMatch(t *testing.T) {
	got, err := GateMatchFromFileMatch(`path startsWith "a/path/" and path endsWith ".md"`)
	if err != nil || got != `event.path startsWith "a/path/" and event.path endsWith ".md"` {
		t.Errorf("expression: %q, %v", got, err)
	}
	got, err = GateMatchFromFileMatch("memories/**/*.md")
	if err != nil || !strings.HasPrefix(got, "event.path matches ") {
		t.Errorf("glob: %q, %v", got, err)
	}
	if _, err := GateMatchFromFileMatch(`any(markers, .kind == "x")`); err == nil {
		t.Error("a match reading markers was converted instead of left to hand")
	}
}
