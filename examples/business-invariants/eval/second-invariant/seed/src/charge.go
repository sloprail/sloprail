package billing

func Charge(total int) error {
	if total < 0 {
		panic("never negative")
	}
	return nil
}
