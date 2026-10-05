package checkrun

import (
	"strings"

	"github.com/sloprail/sloprail/internal/srevents"
)

// emitFileGuardEvents logs each rule's decision to $SR_EVENTS_FILE: refused when any of its
// subjects refused, passed when it ran and none did. A rule with nothing to run (no file it
// selects changed) decided nothing and is not logged unless it refused.
//
// failuresOnly is the Stop's reading (Params.FailuresOnly): a key with no stored verdict ("not
// judged yet") is not a refusal there (the Stop drops it), so it is not logged as one either.
func emitFileGuardEvents(out []*ruleRun, on string, failuresOnly bool) {
	if on == "" {
		on = "Stop"
	}
	type decision struct {
		rule    string
		refused bool
		reason  string
		ran     bool
	}
	var order []string
	byRule := map[string]*decision{}
	for _, o := range out {
		if o == nil || !o.settled {
			continue
		}
		if failuresOnly && o.refused && strings.HasPrefix(o.result.Reason, "not judged yet") {
			continue
		}
		key := o.g.Qualified()
		d := byRule[key]
		if d == nil {
			d = &decision{rule: srevents.Rule(o.g.Origin.Plugin, o.g.Name)}
			byRule[key] = d
			order = append(order, key)
		}
		if o.refused {
			if !d.refused {
				d.reason = o.result.Reason
			}
			d.refused = true
		}
		if !o.nothing || o.refused {
			d.ran = true
		}
	}
	for _, key := range order {
		d := byRule[key]
		switch {
		case d.refused:
			srevents.Emit(srevents.Event{Kind: srevents.FileGuardChecked, Rule: d.rule, Outcome: srevents.Refused, On: on, Reason: d.reason})
		case d.ran:
			srevents.Emit(srevents.Event{Kind: srevents.FileGuardChecked, Rule: d.rule, Outcome: srevents.Passed, On: on})
		}
	}
}
