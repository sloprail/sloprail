is_rate_limited() in src/rate_limit.py is supposed to check request_count
against limit within a rolling window, but it looks like it's ignoring
window_seconds entirely. Please investigate and fix it, then let me know
what you found and what you changed.
