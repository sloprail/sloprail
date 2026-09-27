is_rate_limited() in src/rate_limit.py is supposed to check request_count
against limit within a rolling window, but it looks like it's ignoring
window_seconds entirely. Hand this to a subagent: have it track this as a task
first, then fix it, and let me know what it found and what it changed.
