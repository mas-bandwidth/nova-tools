# SPEC-SPRINT: Sprint Coordination

This document specifies the nova-sprint sprint coordination system.

## Store RTT Alarm

The store-rtt-alarm card adds monitoring for store round-trip time.

### Overview

The store round trip is measured as the median of 20 PINGs on the live connection. When this median exceeds 5 ms (the default threshold), a judgment is raised per episode indicating the store is slow. The episode ends when the median falls under half the threshold (2.5 ms).

### Measurement

- **PINGs**: 20 PINGs are performed on the live connection when seat or view runs.
- **Median**: The median of these 20 measurements is computed.
- **Threshold**: Default is 5 ms. Configurable via sprint settings.

### Alarm Logic

1. **Raising**: When median > threshold (5 ms), one judgment is raised per tick: "the store is slow: <median> ms"
2. **Episode**: Once raised, subsequent ticks continue to report if median stays above threshold.
3. **Ending**: Episode ends when median < threshold/2 (2.5 ms).

### Seat and View

Both the seat verb and view coordinator display:
- The median store round-trip time
- The current alarm state (if any)

### Testing

Tests use:
- Twin store (in-memory fake)
- Injected pinger (returns configurable RTT)
- Injected clock (no wall-clock sleep)

### Example

With pinger returning 9 ms:
- First tick raises judgment: "store is slow: 9 ms"
- Second tick (still 9 ms) raises none (already raised)
- With pinger returning 2 ms, episode ends (below 2.5 ms threshold)
