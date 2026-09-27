def is_rate_limited(request_count, window_seconds, limit):
    """Return True if request_count exceeds limit within window_seconds."""
    return request_count > limit
