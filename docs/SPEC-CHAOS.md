# Chaos Harness (internal/chaos)

This document describes the reusable chaos harness and the friend suite built on it.

## Harness (internal/chaos)

The harness is general-purpose and never friend-specific. It injects faults, polls for recovery on an injected clock, heals, and reports. Each fault has:

- **Name**: A short identifier
- **Inject**: A function that causes the failure
- **Heal**: A function that restores normal operation (optional)
- **Recovered**: A predicate that returns true when the system has recovered
- **Bound**: The maximum time allowed for recovery

The harness runs faults in order and prints one report line per fault:

```
CHAOS <suite> <fault> recovered=<bool> took=<d> bound=<d>
```

An optional Invariant hook is checked after each fault. If it returns an error, the fault is reported as failed.

### Unit tests

The unit tier tests the harness with fake faults and a fake clock. It opens no socket and sleeps on no wall clock. A fault that recovers inside its bound passes; one past it fails with the report line; a broken invariant fails.

## Friend Suite (internal/friend/chaos_functional_test.go)

The functional tier runs in a container through tools/functionalrun. It uses a real nova-friend daemon on a throwaway bus and a fake harness standing in for the session.

### Faults and bounds

1. **Daemon abruptly stopped (SIGKILL)**: The test sends SIGKILL to its own child (session). Recovery: session pong within 1m.

2. **Session process abruptly ended**: The session process is killed. Recovery: session-recovery within 5m.

3. **Bus dropped (Redis stopped and restarted)**: The test's own child Redis is stopped and restarted. Recovery: every pending message delivered within 2m of its return, none lost.

4. **Context filled**: The fake harness answers a context-limit error. Recovery: a fresh session with the handoff within 5m.

5. **Rate limit injected**: The fake harness answers a usage limit with a reset 30s ahead. Recovery: down within 1m, up within 1m of the reset.

### Invariants

- No message lost
- No message pushed twice

### Report lines

The suite prints five CHAOS lines, one per fault, each with recovered=true inside its bound.
