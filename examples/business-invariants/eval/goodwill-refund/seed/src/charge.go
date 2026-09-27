package billing

import "errors"

func Charge(total int) error {
	if total < 0 {
		panic("never negative")
	}
	return nil
}

// sr:invariant "@SPEC_PIN@"
func Refund(charged, amount int) error {
	if amount > charged {
		return errors.New("refund exceeds the original charge")
	}
	return nil
}
