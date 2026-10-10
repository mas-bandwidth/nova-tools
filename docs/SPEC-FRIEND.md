# SPEC: Friend's Row Working Set Equals Live Lanes

## Overview

This specification defines the contract that a friend's row working set must equal her live lanes. This ensures that:
1. A friend only works on cards that are properly taken on their row
2. The server can accurately track which cards are being worked on
3. Work is properly reported through heartbeat beats

## Definitions

### Row Working Set
The set of cards currently assigned to a friend's row, managed by the daemon.

### Live Lanes
The set of card IDs that a friend is actively working on, reported through heartbeat beats.

### Card
A unit of work identified by a unique card ID.

### Lane
A worker thread or process that performs work on a card.

## Rules

### 1. Lane Start Restriction
A lane starts only on a card the daemon has taken on the friend's row. The card ID is:
- Stored in the lane's environment variables
- Stored in the lane's job directory
- Used to track lane lifecycle

A lane ends when there is a finish or fail on that card.

### 2. Beat Reporting
Every friend beat must list her live lanes with their card IDs:
```
nova-friend beat --lanes card-id-1,card-id-2,...
```

The server records this list as the source of truth for what work is actually being done.

### 3. Server Enforcement
The server holds the friend's row working set to match their live lanes:

#### 3.1. Stale Card Expiration
A taken card with no live lane past its take deadline is automatically moved back to ready status.

#### 3.2. Unauthorized Work Judgment
A live lane on a card not taken on the friend's row is a judgment naming:
- The card ID
- The lane ID

This judgment is recorded and can trigger alerts or work reassignment.

## Data Models

### LiveLane
```
{
  id: string     // card ID
  target: string // what the lane is working on
}
```

### TakeRecord
```
{
  friend: string    // friend identifier
  card_id: string   // card being taken
  taken_at: time    // when taken
  deadline: time    // take deadline
}
```

### Judgment
```
{
  card_id: string    // card involved
  lane_id: string    // lane involved
  reason: string     // description of violation
  severity: string   // warning or error
}
```

## API

### Beat Endpoint
Records a friend's live lanes.

**Request:**
```
POST /friend/{friend}/beat
{
  "lanes": ["card-id-1", "card-id-2", ...]
}
```

**Response:**
```
{
  "recorded": true,
  "lanes": ["card-id-1", "card-id-2", ...]
}
```

### Validation Endpoint
Validates that row working set equals live lanes.

**Request:**
```
GET /friend/{friend}/validate
```

**Response:**
```
{
  "judgments": [
    {
      "card_id": "card-123",
      "lane_id": "lane-456",
      "reason": "live lane on card not taken on row",
      "severity": "error"
    }
  ],
  "stale_cards": ["card-789"]
}
```

## Tests

Socket-free tests verify:
1. Lane start only occurs on taken cards
2. Beat reporting records live lanes
3. Server enforcement identifies stale cards and unauthorized work

## Related Specifications

- `internal/nsprint/taskcard/` - Card state management
- `internal/nsprint/friend/` - Friend state and ladder
- `internal/swarm/` - Worker management
