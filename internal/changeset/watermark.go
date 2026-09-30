package changeset

import "fmt"

// PickWatermark chooses a rule's watermark from the heads it passed at, newest
// first (checkstore's PassedHeads): the first one reachable — an ancestor of
// HEAD — and, when a newer one had to be passed over, that newer one as dropped.
//
// The watermark is DERIVED from the recorded runs and is never stored on its
// own, so an amend or a rebase that orphans the newest pass simply makes the
// next-newest the answer, or none. Reachability is asked of `reachable`, so this
// stays free of git; an error from it is returned, never read as "unreachable":
// a head git could not be asked about is not one that can be skipped.
func PickWatermark(heads []string, reachable func(sha string) (bool, error)) (watermark, dropped string, err error) {
	for _, h := range heads {
		ok, err := reachable(h)
		if err != nil {
			return "", "", fmt.Errorf("changeset: is %s reachable: %w", h, err)
		}
		if ok {
			return h, dropped, nil
		}
		if dropped == "" {
			dropped = h
		}
	}
	return "", dropped, nil
}
