# Hue Bridge Request Optimization

## Current Techniques

### 1. Event-driven state sync
- `startEventStream` opens the Hue SSE endpoint and `handleEventBatch` applies every `grouped_light` update to the cached `d.light` struct.
- Once the initial `GetGroupedLightById` call succeeds there are no more polling GET requests—the daemon learns about on/off and brightness changes reactively.
- This keeps the bridge load to roughly one persistent HTTPS connection plus the PUTs that correspond to user commands.

### 2. Optimistic local updates
- `toggle` and `adjustBrightness` mutate the in-memory `d.light` state immediately after each PUT.
- Subsequent commands therefore rely on the cached values without waiting for the bridge to echo the new state, giving instant feedback and eliminating follow-up reads.

### 3. Read-only status responses
- The `status` command simply formats the cached brightness, so users can query the daemon without touching the bridge at all.
- Logging the current brightness after every mutating command provides easy observability while keeping the request count unchanged.

## Request Profile

| Scenario | Before optimization | Current behavior |
| --- | --- | --- |
| Single toggle | `GET` (fetch state) + `PUT` | `PUT` only (state already cached) |
| Brightness change | `GET` + `PUT` | `PUT` only |
| Rapid sequence | `N` commands → `2N` requests | `N` commands → `N` requests (after first sync) |

In practice the daemon establishes one SSE stream and then emits exactly one PUT per CLI command, which keeps Hue bridges well below their documented 10 req/s guidance.

## Opportunities for Further Optimization

1. **Resume-aware event streaming** – propagate the last SSE `id` back into `startEventStream` so reconnects continue from the previous cursor rather than starting fresh.
2. **Graceful shutdown and backoff control** – allow the daemon to cancel the SSE goroutine and use exponential backoff (via `context.Context` and `net/http` timeouts) instead of the fixed 5-second sleep.
3. **Command batching/debouncing** – coalesce multiple `up/down` commands that arrive within a short window before issuing a single PUT to the bridge.


These steps would tighten correctness around reconnects, improve reliability, and squeeze out a few more requests without sacrificing responsiveness.
