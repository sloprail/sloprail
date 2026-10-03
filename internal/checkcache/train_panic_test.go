package checkcache

import "testing"

// The dictionary builder panics on some sample sets (seen live: slice bounds out of range
// [-65538:] during an automatic Gc). trainDict must turn that into an error.
func TestTrainDictNeverPanics(t *testing.T) {
	sets := [][][]byte{
		nil,
		{[]byte("x")},
		{make([]byte, 200<<10)},
		func() [][]byte {
			var s [][]byte
			for i := 0; i < 50; i++ {
				s = append(s, make([]byte, 70<<10))
			}
			return s
		}(),
	}
	for i, samples := range sets {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("set %d: trainDict panicked: %v", i, r)
				}
			}()
			_, _ = trainDict(samples)
		}()
	}
}
