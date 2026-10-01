package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// RULE AGE: a rule judges only commits made after it came into force for the session.
//
// A project rule's age is in its repository history (the commit that added or last changed
// it, gitrepo.ResolveRange). A rule that is in no commit's history (a plugin's, installed or
// enabled mid-session) has no such record, so the engine keeps one: the first hook that
// loads a rule notes it. The first hook of the session notes every rule it finds as present
// "at start" (they are in force for the whole session), and a rule first found by a later
// hook is noted with that time. Commits committed before it are not that rule's debt, for
// HEAD and for every other recorded tip alike.

// recordRulesSeen notes, for each file-guard loaded, when a hook first saw it. Best effort:
// a failure costs only the age floor of one rule, and is reported.
func recordRulesSeen(cmd *cobra.Command, state sessionstate.Store, guards []declaration.FileGuard) {
	if state == nil || len(guards) == 0 {
		return
	}
	warn := func(err error) { fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: rule ages not recorded: %v\n", err) }
	_, initialized, err := state.Meta(sessionstate.MetaRulesSeenInit)
	if err != nil {
		warn(err)
		return
	}
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	for _, g := range guards {
		key := sessionstate.MetaRuleSeenPrefix + g.Qualified()
		if _, had, err := state.Meta(key); err != nil {
			warn(err)
			return
		} else if had {
			continue
		}
		value := now
		if !initialized {
			value = sessionstate.RuleSeenAtStart
		}
		if err := state.SetMeta(key, value); err != nil {
			warn(err)
			return
		}
	}
	if !initialized {
		if err := state.SetMeta(sessionstate.MetaRulesSeenInit, now); err != nil {
			warn(err)
		}
	}
}

// ruleInForceSince is when a rule first came into force for the session, or the zero time
// when it was there from the start (or nothing is known, which keeps the stricter range).
func ruleInForceSince(state sessionstate.Store, g declaration.FileGuard) time.Time {
	if state == nil {
		return time.Time{}
	}
	v, ok, err := state.Meta(sessionstate.MetaRuleSeenPrefix + g.Qualified())
	if err != nil || !ok || v == sessionstate.RuleSeenAtStart {
		return time.Time{}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(0, n)
}
