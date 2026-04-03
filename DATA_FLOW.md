# Almanac v3 Data Flow

Canonical websocket and signing protocol for the Almanac backend/frontend pair.

## Network

- Network: Monad mainnet
- Chain ID: `143`

## Signing

Frontend clients do **not** send on-chain transactions directly.
They sign EIP-712 payloads and send the signed payloads over websocket.
Backend verifies signatures, manages session identity, validates bets, and is the future owner of on-chain payment execution.

## EIP-712 Domain

```json
{
  "name": "Almanac",
  "version": "1",
  "chainId": 143
}
```

No `verifyingContract` is currently used.

## EIP-712 Types

### SignIn

```solidity
SignIn(address wallet,uint256 timestamp,bool simulation)
```

Field semantics:
- `wallet`: wallet that owns the websocket session
- `timestamp`: unix timestamp in seconds
- `simulation`: `false` for regular (real-money) mode, `true` for simulation mode

Validation rules:
- signature must recover to `wallet`
- `abs(now - timestamp) <= 60`
- `simulation` determines connection lane (regular or sim); both values are accepted

### Bet

```solidity
Bet(address wallet,string gameId,string roundId,uint256 nonce,uint256 timestamp,uint256 amount,int256 x,int256 y,uint256 betRadius,bool simulation,uint256 minimumMultiplier)
```

Field semantics:
- `wallet`: bettor wallet
- `gameId`: ESPN game identifier
- `roundId`: current unresolved play identifier inside the game feed
- `nonce`: per-wallet strictly unique nonce
- `timestamp`: unix timestamp in seconds
- `amount`: bet amount in **cents** (`$5.00` => `500`)
- `x`: coordinate scaled by `1000`
- `y`: coordinate scaled by `1000`
- `betRadius`: win radius scaled by `1000`
- `simulation`: `false` for regular mode, `true` for simulation mode
- `minimumMultiplier`: minimum acceptable payout multiplier, where `1` means at least `1x` profit / `2x` total return on win

Validation rules:
1. signature must recover to `wallet`
2. `abs(now - timestamp) <= 30`
3. nonce must not already be used by that wallet
4. `minimumMultiplier <= actualMultiplier`
5. `roundId` must reference a known unresolved play in current game state
6. `simulation` must match the connection's established lane (set during `signin`)
7. wallet balance must be `>= amount`
8. amount must be `>= minimumBetAmount`

Current backend constants:
- `minimumBetAmount = 1.0` USD in websocket payloads / `100` cents in EIP-712 amount
- `actualMultiplier = 1`
- `default betRadius = 35.0`

## Websocket Lifecycle

1. Client opens websocket connection.
2. Server may immediately push `game_state`.
3. Client signs and sends `signin`.
4. Server verifies `signin`.
5. On success server sends `signin_ack` to that connection and marks the connection wallet-bound.
6. After `signin_ack`, client may flush queued outbound messages, including `place_bet`.
7. Server sends `bet_ack` for each bet submission.
8. When the referenced play resolves, server sends `bet_result` to connections associated with that wallet.
9. Client sends `ping` every 5 seconds.
10. Server replies with `pong` echoing the client timestamp.

Backward compatibility:
- `subscribe_wallet` is **disabled** — the backend returns an explicit error if received (as of commit `2983d0d`). All session identity is established exclusively through signed `signin` messages.

## Message Envelope

All websocket messages are plain JSON objects with a top-level `type` field.

```json
{ "type": "..." }
```

## Client → Server Messages

### `signin`

```json
{
  "type": "signin",
  "wallet": "0x1234...",
  "signature": "0x...",
  "timestamp": 1711900000,
  "simulation": false
}
```

Schema:

```json
{
  "type": "object",
  "required": ["type", "wallet", "signature", "timestamp", "simulation"],
  "properties": {
    "type": { "const": "signin" },
    "wallet": { "type": "string", "pattern": "^0x[a-fA-F0-9]{40}$" },
    "signature": { "type": "string", "pattern": "^0x[a-fA-F0-9]{130}$" },
    "timestamp": { "type": "integer", "minimum": 1 },
    "simulation": { "type": "boolean" }
  },
  "additionalProperties": false
}
```

### `place_bet`

Websocket payload uses **raw float values** for coordinates, radius, and dollar amount.
The backend is responsible for converting them into the EIP-712 integer representation when verifying.

```json
{
  "type": "place_bet",
  "wallet": "0x1234...",
  "gameId": "401234567",
  "roundId": "401234567_play_89",
  "nonce": 42,
  "timestamp": 1711900000,
  "amount": 5.0,
  "x": 250.0,
  "y": 200.0,
  "betRadius": 35.0,
  "simulation": false,
  "minimumMultiplier": 1,
  "signature": "0x..."
}
```

Schema:

```json
{
  "type": "object",
  "required": [
    "type",
    "wallet",
    "gameId",
    "roundId",
    "nonce",
    "timestamp",
    "amount",
    "x",
    "y",
    "betRadius",
    "simulation",
    "minimumMultiplier",
    "signature"
  ],
  "properties": {
    "type": { "const": "place_bet" },
    "wallet": { "type": "string", "pattern": "^0x[a-fA-F0-9]{40}$" },
    "gameId": { "type": "string", "minLength": 1 },
    "roundId": { "type": "string", "minLength": 1 },
    "nonce": { "type": "integer", "minimum": 1 },
    "timestamp": { "type": "integer", "minimum": 1 },
    "amount": { "type": "number", "exclusiveMinimum": 0 },
    "x": { "type": "number" },
    "y": { "type": "number" },
    "betRadius": { "type": "number", "exclusiveMinimum": 0 },
    "simulation": { "type": "boolean" },
    "minimumMultiplier": { "type": "integer", "minimum": 1 },
    "signature": { "type": "string", "pattern": "^0x[a-fA-F0-9]{130}$" }
  },
  "additionalProperties": false
}
```

### `ping`

```json
{
  "type": "ping",
  "timestamp": 1711900005123
}
```

Schema:

```json
{
  "type": "object",
  "required": ["type", "timestamp"],
  "properties": {
    "type": { "const": "ping" },
    "timestamp": { "type": "integer", "minimum": 1 }
  },
  "additionalProperties": false
}
```

### Legacy messages (disabled)

- `subscribe_wallet` — **disabled**, returns error. All identification uses signed `signin`.
- `subscribe_game` — no longer part of the canonical protocol.

## Server → Client Messages

### `signin_ack`

Sent only to the successfully signed-in connection.

```json
{
  "type": "signin_ack",
  "wallet": "0xABC...",
  "balance": 0.0,
  "nextNonce": 1,
  "minimumBetAmount": 1.0,
  "gameRadii": {
    "401234567": 35.0,
    "401234568": 35.0
  },
  "betHistory": [
    {
      "betId": "bet-123",
      "gameId": "401234567",
      "roundId": "401234567_play_89",
      "nonce": 1,
      "amount": 5.0,
      "x": 250.0,
      "y": 200.0,
      "betRadius": 35.0,
      "minimumMultiplier": 1,
      "actualMultiplier": 1,
      "status": "win",
      "placedAt": "2026-04-01T00:00:00Z",
      "simulation": false,
      "payout": 10.0,
      "nullificationReason": "",
      "rejectionReason": "",
      "isHistorical": true
    }
  ]
}
```

Schema:

```json
{
  "type": "object",
  "required": ["type", "wallet", "balance", "simulation", "nextNonce", "minimumBetAmount", "gameRadii", "betHistory"],
  "properties": {
    "type": { "const": "signin_ack" },
    "wallet": { "type": "string", "pattern": "^0x[a-fA-F0-9]{40}$" },
    "balance": { "type": "number" },
    "simulation": { "type": "boolean" },
    "nextNonce": { "type": "integer", "minimum": 1 },
    "minimumBetAmount": { "type": "number", "minimum": 0 },
    "gameRadii": {
      "type": "object",
      "additionalProperties": { "type": "number", "exclusiveMinimum": 0 }
    },
    "betHistory": {
      "type": "array",
      "items": {
        "type": "object",
        "required": [
          "betId",
          "gameId",
          "roundId",
          "nonce",
          "amount",
          "x",
          "y",
          "betRadius",
          "minimumMultiplier",
          "actualMultiplier",
          "status",
          "placedAt",
          "simulation",
          "payout",
          "nullificationReason",
          "rejectionReason",
          "isHistorical"
        ],
        "properties": {
          "betId": { "type": "string" },
          "gameId": { "type": "string" },
          "roundId": { "type": "string" },
          "nonce": { "type": "integer", "minimum": 1 },
          "amount": { "type": "number", "minimum": 0 },
          "x": { "type": "number" },
          "y": { "type": "number" },
          "betRadius": { "type": "number", "exclusiveMinimum": 0 },
          "minimumMultiplier": { "type": "integer", "minimum": 1 },
          "actualMultiplier": { "type": "integer", "minimum": 0 },
          "status": {
            "type": "string",
            "enum": ["win", "loss", "nullified", "live", "invalid", "pending"]
          },
          "placedAt": { "type": "string", "format": "date-time" },
          "simulation": { "type": "boolean" },
          "payout": { "type": "number", "minimum": 0 },
          "nullificationReason": { "type": "string" },
          "rejectionReason": { "type": "string" },
          "isHistorical": { "type": "boolean" }
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}
```

### `bet_ack`

Sent only to the connection that submitted the bet.

```json
{
  "type": "bet_ack",
  "status": "accepted",
  "gameId": "401234567",
  "nonce": 42,
  "timestamp": 1711900005,
  "balance": 95.0,
  "actualMultiplier": 1,
  "simulation": false,
  "rejectionReason": ""
}
```

Schema:

```json
{
  "type": "object",
  "required": ["type", "status", "gameId", "nonce", "timestamp", "balance", "actualMultiplier", "rejectionReason", "simulation"],
  "properties": {
    "type": { "const": "bet_ack" },
    "status": { "type": "string", "enum": ["accepted", "rejected"] },
    "gameId": { "type": "string" },
    "nonce": { "type": "integer", "minimum": 1 },
    "timestamp": { "type": "integer", "minimum": 1 },
    "balance": { "type": "number" },
    "actualMultiplier": { "type": "integer", "minimum": 0 },
    "simulation": { "type": "boolean" },
    "rejectionReason": { "type": "string" }
  },
  "additionalProperties": false
}
```

### `play_event`

Emitted when the backend observes or replays a play.

```json
{
  "type": "play_event",
  "game_id": "401234567",
  "play_id": "401234567_play_89",
  "sport": "nba",
  "timestamp": "2026-04-01T02:00:08Z",
  "location": { "x": 250.0, "y": 200.0 },
  "event": {
    "description": "Jalen Suggs makes 21-foot jumper",
    "team": "ORL"
  },
  "simulation": false
}
```

### `bet_result`

Sent to all active connections bound to the winning/losing wallet when the play resolves.

```json
{
  "type": "bet_result",
  "outcome": "win",
  "wallet": "0xABC...",
  "nonce": 42,
  "gameId": "401234567",
  "roundId": "401234567_play_89",
  "betCoordinates": { "x": 250.0, "y": 200.0 },
  "betRadius": 35.0,
  "backendTimestamp": 1711900010,
  "eventTimestamp": 1711900008,
  "amountBet": 5.0,
  "amountWon": 10.0,
  "simulation": false,
  "balance": 105.0,
  "isHistorical": false,
  "nullificationReason": ""
}
```

Schema:

```json
{
  "type": "object",
  "required": [
    "type",
    "outcome",
    "wallet",
    "nonce",
    "gameId",
    "roundId",
    "betCoordinates",
    "betRadius",
    "backendTimestamp",
    "eventTimestamp",
    "amountBet",
    "amountWon",
    "simulation",
    "balance",
    "isHistorical",
    "nullificationReason"
  ],
  "properties": {
    "type": { "const": "bet_result" },
    "outcome": { "type": "string", "enum": ["win", "loss", "nullified"] },
    "wallet": { "type": "string", "pattern": "^0x[a-fA-F0-9]{40}$" },
    "nonce": { "type": "integer", "minimum": 1 },
    "gameId": { "type": "string" },
    "roundId": { "type": "string" },
    "betCoordinates": {
      "type": "object",
      "required": ["x", "y"],
      "properties": {
        "x": { "type": "number" },
        "y": { "type": "number" }
      },
      "additionalProperties": false
    },
    "betRadius": { "type": "number", "exclusiveMinimum": 0 },
    "backendTimestamp": { "type": "integer", "minimum": 1 },
    "eventTimestamp": { "type": "integer", "minimum": 1 },
    "amountBet": { "type": "number", "minimum": 0 },
    "amountWon": { "type": "number", "minimum": 0 },
    "simulation": { "type": "boolean" },
    "balance": { "type": "number" },
    "isHistorical": { "type": "boolean" },
    "nullificationReason": { "type": "string" }
  },
  "additionalProperties": false
}
```

### `game_state`

Sent on connect and periodically thereafter.
Only games whose start time is within the last 24 hours should be included in outbound payloads.
No in-memory deletion is implied by that filter.

```json
{
  "type": "game_state",
  "simulation": false,
  "games": [
    {
      "game_id": "401234567",
      "sport": "nba",
      "status": "in_progress",
      "state": "in",
      "start_time": "2026-04-01T02:00:00Z",
      "home": "LAL",
      "away": "BOS",
      "home_score": 88,
      "away_score": 91,
      "period": "Q4",
      "clock": "02:13",
      "possession": "LAL"
    }
  ]
}
```

Expected game object fields:
- `game_id`: string
- `sport`: `nba | mlb | golf`
- `status`: ESPN-derived status string, e.g. `in_progress`, `final`, `pre`, `scheduled`, `post`, etc.
- `state`: raw ESPN state when available (`in`, `pre`, `post`, etc.)
- `start_time`: ISO-8601 timestamp
- team and score fields as available
- `simulation`: present on simulation-lane game objects
- sport-specific metadata may be included

Top-level message fields:
- `type = "game_state"`
- `games`: array of normalized game objects
- `simulation`: lane marker for the broadcast (`false` regular, `true` simulation)

### `pong`

```json
{
  "type": "pong",
  "timestamp": 1711900005123
}
```

Schema:

```json
{
  "type": "object",
  "required": ["type", "timestamp"],
  "properties": {
    "type": { "const": "pong" },
    "timestamp": { "type": "integer", "minimum": 1 }
  },
  "additionalProperties": false
}
```

### `error`

Used for fatal protocol errors such as invalid signin.

```json
{
  "type": "error",
  "message": "invalid signin signature"
}
```

Schema:

```json
{
  "type": "object",
  "required": ["type", "message"],
  "properties": {
    "type": { "const": "error" },
    "message": { "type": "string" }
  },
  "additionalProperties": false
}
```

Invalid signin handling:
- server sends the error above
- server closes the websocket connection immediately after the send

## Units and Conversions

### Websocket payload units
- `amount`: dollars as number, e.g. `5.0`
- `x`, `y`: raw frontend coordinate floats
- `betRadius`: raw frontend radius float

### Signed payload units
- `amount`: cents (`amount * 100`)
- `x`, `y`: coordinate scaled by `1000`
- `betRadius`: radius scaled by `1000`

## Historical vs Live Bets

- `signin_ack.betHistory[*].isHistorical` must be `true`
- live pushed `bet_result.isHistorical` must be `false`
- historical entries should render without celebratory/pulse animation

## Current backend payout behavior

- `actualMultiplier = 1`
- win total return = `amountBet * 2`
- `amountWon = amountBet * 2` on win
- `amountWon = 0` on loss or nullification

## Notes for future contributors

- Keep frontend websocket field names and backend JSON tags exactly aligned.
- If the protocol changes, update this file in both repos in the same change set.
- Prefer extending these message types over introducing parallel shapes for the same concept.
