The file `payments/user.go` has grown into a grab-bag of read and write
operations on users. Split it into two files: `payments/user_reads.go` for
the read-only functions (`GetUser`, `ListActiveUsers`) and
`payments/user_writes.go` for the functions that mutate a user
(`UpdateEmail`, `CancelPlan`). Keep the `User` struct in `user_reads.go`.
Behavior must stay identical — this is a pure move, not a rewrite. Delete
the functions from `user.go` once they're moved so nothing is duplicated.
