SPEC.md's second rule ("a refund must never exceed the original charge
amount") isn't enforced anywhere in the code yet. Add a `Refund(charged,
amount int) error` function alongside `Charge` in src/charge.go that
enforces it.
