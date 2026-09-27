## v2.3.0

- The `retry` option now defaults to 3 attempts (was 1).
- Fixed a bug where `timeout` in milliseconds was silently treated as
  seconds on Windows.
- `connect()` now rejects an empty `host` argument with a clear error
  instead of hanging.

## v2.2.0

- Added a `pool_size` option to `connect()`, default 10.
- Deprecated the `legacy_auth` flag; it will be removed in v3.0.0.
