# Timing and TTL Dependencies

This document outlines the critical time-based configurations in the system and their dependencies. Misconfiguring these values can lead to instability, such as premature worker shutdowns or zombie worker records in Redis.

## Summary

| Parameter | Location | Default | Depends On | Notes |
|---|---|---|---|---|
| Worker Heartbeat Interval | `worker/src/config.ts` | 10s | - | The fundamental pulse of the system. |
| Worker Key TTL | `worker/src/config.ts` | 30s | Worker Heartbeat | Must be > Heartbeat Interval. Recommended: 2x. |
| Shutdown Command TTL | `proxy/pkg/config/config.go` | 60s | Worker Heartbeat | Must be > Heartbeat Interval. Recommended: 4x. |
| Worker Drain Timeout | `worker/src/index.ts` | 30s | - | Safety net to force-kill a stuck draining worker. |
| Reaper Stale Threshold | `proxy/internal/redis/reaper.lua` | 30s | Worker Heartbeat | Must be > Heartbeat Interval. Recommended: 3x. |
| Reaper Run Interval | `proxy/pkg/config/config.go` | 300s (5m) | - | How often the proxy checks for dead workers. |

---

## Detailed Explanations

### 1. Worker Heartbeat Interval
- **Purpose**: How often a living worker process sends a "I'm still here" signal to Redis.
- **File**: `worker/src/config.ts` (`server.heartbeatInterval`)
- **Default**: `10000` (10 seconds)
- **Dependencies**: This is the core value upon which many other timeouts are based.

### 2. Worker Key TTL
- **Purpose**: The Time-To-Live for the main `worker:{id}` key in Redis. Each heartbeat refreshes this TTL. If it expires, the worker is considered dead.
- **File**: `worker/src/config.ts` (`redis.keyTtl`)
- **Default**: `30` (30 seconds)
- **Dependency Rule**: `Worker Key TTL` **must** be greater than `Worker Heartbeat Interval`.
- **Rationale**: If the TTL is shorter than the heartbeat interval, the worker's key could expire before it has a chance to send its next heartbeat, making it appear dead even though it's healthy.
- **Recommendation**: Set `Worker Key TTL` to be at least **2x** the `Worker Heartbeat Interval`.

### 3. Shutdown Command TTL
- **Purpose**: The TTL for the `worker:cmd:{id}` key created by the proxy. This is how long a shutdown command will wait in Redis to be picked up by the worker.
- **File**: `proxy/pkg/config/config.go` (`SHUTDOWN_COMMAND_TTL`)
- **Default**: `60` (60 seconds)
- **Dependency Rule**: `Shutdown Command TTL` **must** be greater than `Worker Heartbeat Interval`.
- **Rationale**: The worker only checks for commands during its heartbeat. If the command's TTL is shorter than the heartbeat interval, the command could expire before the worker has a chance to see it.
- **Recommendation**: Set `Shutdown Command TTL` to be at least **4x** the `Worker Heartbeat Interval` to withstand transient network issues.

### 4. Worker Drain Timeout
- **Purpose**: A safety mechanism. If a worker is in the `'draining'` state for too long (e.g., a connection hangs and never closes), this timeout will force the worker to shut down anyway.
- **File**: `worker/src/index.ts` (hardcoded)
- **Default**: `30000` (30 seconds)
- **Dependencies**: None. This is a standalone safety net.

### 5. Reaper Stale Threshold
- **Purpose**: How long a worker can go without a heartbeat before the proxy's reaper process considers it "stale" or "dead" and cleans up its data.
- **File**: `proxy/internal/redis/reaper.lua` (hardcoded)
- **Default**: `30` (30 seconds)
- **Dependency Rule**: `Reaper Stale Threshold` **must** be significantly greater than `Worker Heartbeat Interval`.
- **Rationale**: You need to allow for some network latency or a missed heartbeat or two without prematurely reaping a healthy worker.
- **Recommendation**: Set `Reaper Stale Threshold` to be at least **3x** the `Worker Heartbeat Interval`. *Note: This should be moved to the proxy configuration file.*

### 6. Reaper Run Interval
- **Purpose**: How often the reaper process runs to find and clean up stale workers.
- **File**: `proxy/pkg/config/config.go` (`REAPER_RUN_INTERVAL`)
- **Default**: `300` (300 seconds, or 5 minutes)
- **Dependencies**: Should ideally be greater than the `Reaper Stale Threshold` to avoid running unnecessarily, but it's not a strict dependency. 