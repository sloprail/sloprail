package repo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every `sr-session <sub>` the plugin's hooks run is a lifecycle entry point an agent must not run
// itself (it could forge a session's signals). The no-lifecycle-commands gate must name each one,
// in both the `sr-session <sub>` and the `sr session <sub>` spellings, so a new hook subcommand
// added without gate coverage fails here.
func TestLifecycleGateCoversEveryHookSubcommand(t *testing.T) {
	plugin := filepath.Join("..", "..", "marketplace", "plugins", "sloprail")
	raw, err := os.ReadFile(filepath.Join(plugin, "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var hooks struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &hooks); err != nil {
		t.Fatal(err)
	}
	invoked := regexp.MustCompile(`sr-session(?:-hook\.sh)?"?\s+([a-z][a-z-]*)`)
	subs := map[string]bool{}
	for _, entries := range hooks.Hooks {
		for _, e := range entries {
			for _, h := range e.Hooks {
				if m := invoked.FindStringSubmatch(h.Command); m != nil {
					subs[m[1]] = true
				}
			}
		}
	}
	if len(subs) == 0 {
		t.Fatal("found no sr-session subcommand in hooks.json: the extraction is broken")
	}

	gateRaw, err := os.ReadFile(filepath.Join(plugin, ".sloprail", "gate", "no-lifecycle-commands", "gate.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var gate struct {
		On []struct {
			Match string `yaml:"match"`
		} `yaml:"on"`
	}
	if err := yaml.Unmarshal(gateRaw, &gate); err != nil || len(gate.On) == 0 {
		t.Fatalf("gate.yaml: %v", err)
	}
	match := gate.On[0].Match

	var names []string
	for s := range subs {
		names = append(names, s)
	}
	sort.Strings(names)
	for _, s := range names {
		for _, idx := range []string{".argv[1]", ".argv[2]"} {
			if !strings.Contains(match, idx+` == "`+s+`"`) {
				t.Errorf("the hooks run `sr-session %s` but no-lifecycle-commands does not refuse it via %s", s, idx)
			}
		}
	}
}
