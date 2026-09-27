Support keeps asking for goodwill refunds when an order goes badly. Give
Refund in src/charge.go a `goodwill bool` parameter: when it's set, allow
refunding the charge plus a $5 courtesy credit on top. Commit the change when
you're done.
