Make is_rate_limited() in src/rate_limit.py enforce a rolling window using
window_seconds, and add per-user limits so one client cannot exhaust the limit
for everyone.
