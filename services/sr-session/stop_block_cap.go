package main

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// The refusal loop and its cap.
//
// A Stop the engine refuses comes back as a retry: the harness sends the agent
// round again and fires Stop once more with stop_hook_active set. That retry is
// JUDGED — an agent does not get past a rule by replying twice — so a reply that
// still breaks a rule is refused again, and the loop runs until a reply passes.
//
// It used to be the other way round: any Stop carrying stop_hook_active ended
// un-judged, so every Stop gate held for exactly one refusal and then gave way.
// That was a bypass built into the engine. The project now decides how many
// refusals in a row it will take, as stop_hook_block_cap in .sloprail/config.yaml
// (declaration.StopHookBlockCap), defaulting to the harness's own cap of 8. A
// project that wants the old behaviour writes 1.
//
// The count lives in the session's store (a sub-agent's in its own), because the
// harness reports only WHETHER this is a retry, not how many there have been.

// stopHookBlockCapReached reports whether this Stop should end un-judged because
// the project's cap on consecutive refusals has been reached.
//
// A Stop that is not a retry starts a new sequence, so the count is reset first
// — a sequence the harness itself cut short (its own cap overriding the hook)
// must not carry its count into the next turn.
//
// Plumbing failures do not end the turn un-judged: a count that cannot be read
// is taken as zero and a cap that cannot be read as the default, each reported.
// The rule then keeps judging, and the harness's own cap is still there if the
// loop never ends.
func stopHookBlockCapReached(cmd *cobra.Command, store sessionstate.Store, p HookPayload) bool {
	if !p.StopHookActive {
		resetStopRefusals(cmd, store)
		return false
	}

	limit, err := declaration.StopHookBlockCap(dotDir(p.Cwd))
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: stop_hook_block_cap not read, using %d: %v\n", limit, err)
	}
	if limit == 0 {
		return false
	}

	refusals := stopRefusals(cmd, store)
	if refusals < limit {
		return false
	}

	// Said out loud: a turn that ends here ends with a rule still refusing it.
	fmt.Fprintf(cmd.ErrOrStderr(),
		"sloprail: this Stop was refused %d times in a row, reaching stop_hook_block_cap (%d) — letting the turn end un-judged; the refusal still stands\n",
		refusals, limit)
	resetStopRefusals(cmd, store)
	return true
}

// stopRefusals is the current sequence's count, 0 when unset or unreadable.
func stopRefusals(cmd *cobra.Command, store sessionstate.Store) int {
	v, ok, err := store.Meta(sessionstate.MetaStopRefusals)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: refusal count not read:", err)
		return 0
	}
	if !ok || v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// countStopRefusal records one more refusal in the current sequence.
func countStopRefusal(cmd *cobra.Command, store sessionstate.Store) {
	n := stopRefusals(cmd, store) + 1
	if err := store.SetMeta(sessionstate.MetaStopRefusals, strconv.Itoa(n)); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: refusal count not recorded:", err)
	}
}

// resetStopRefusals ends the current sequence.
func resetStopRefusals(cmd *cobra.Command, store sessionstate.Store) {
	if err := store.SetMeta(sessionstate.MetaStopRefusals, "0"); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: refusal count not reset:", err)
	}
}
