package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

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
// sr:invariant session/turn-end-retry-judged-until-cap
func stopHookBlockCapReached(cmd *cobra.Command, store refusalMeta, p HookPayload) bool {
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
func stopRefusals(cmd *cobra.Command, store refusalMeta) int {
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
// sr:invariant session/turn-end-retry-judged-until-cap
func countStopRefusal(cmd *cobra.Command, store refusalMeta) {
	n := stopRefusals(cmd, store) + 1
	if err := store.SetMeta(sessionstate.MetaStopRefusals, strconv.Itoa(n)); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: refusal count not recorded:", err)
	}
}

// resetStopRefusals ends the current sequence.
// sr:invariant session/turn-end-retry-judged-until-cap
func resetStopRefusals(cmd *cobra.Command, store refusalMeta) {
	if err := store.SetMeta(sessionstate.MetaStopRefusals, "0"); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: refusal count not reset:", err)
	}
}

// refusalMeta is where the refusal count is kept: the session's store, or, when
// that store cannot be opened, the counter file beside it.
type refusalMeta interface {
	Meta(key string) (string, bool, error)
	SetMeta(key, value string) error
}

// refusalCounterFile keeps the refusal count when the session's store is
// unavailable (damaged, unopenable). The loop it bounds does not stop for a
// store that cannot be read: a harness with no cap of its own (Codex) sends the
// agent round again for as long as the Stop refuses, and a refusal caused by the
// unreadable state refuses every time. The count therefore lives in a file next
// to the store, which needs only the same directory.
type refusalCounterFile string

func (f refusalCounterFile) Meta(key string) (string, bool, error) {
	b, err := os.ReadFile(string(f))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(b)), true, nil
}

func (f refusalCounterFile) SetMeta(key, value string) error {
	return os.WriteFile(string(f), []byte(value+"\n"), 0o644)
}

// storelessRefusalCounter is the counter file for the session a payload belongs
// to, and false when even the store's location cannot be derived (then nothing
// keys a count, and the harness's own cap is all there is).
func storelessRefusalCounter(p HookPayload) (refusalMeta, bool) {
	id, err := stableID(p)
	if err != nil {
		return nil, false
	}
	path, err := sessionDBPath(stateCwdOf(p), id)
	if err != nil {
		return nil, false
	}
	return refusalCounterFile(path + ".stop-refusals"), true
}
