# Backend Contract Foundation: ESPN-Only Assumed Possession

This document defines the authoritative backend truth model for the ESPN-only assumed-possession product. It is the contract foundation for campaign item 4.

## Status

- **This item defines the contract.**
- **This item does not claim full runtime emission yet.**
- Item 5 should implement these fields in live websocket payloads and bet persistence using the exact shapes already defined in `internal/game/assumed_possession.go`.

That distinction is intentional. The repo now has explicit backend-owned shapes so later work can implement them directly instead of inventing frontend-only inference.

## Non-negotiable product rule

The frontend must never infer the user's bet meaning ad hoc from scoreboard state, local timers, or UI memory when the backend can state it explicitly.

If the backend says the market is for `LAL`, the product means `LAL`. If the next shot comes from `LAC`, the correct outcome is nullification/refund, not silent reinterpretation.

## Contract version

- `contractVersion = "assumed_possession.v1"`
- Source of truth: backend only
- Initial source authority: `source = "espn"`

## Market states

Defined in `internal/game/assumed_possession.go` as `game.MarketState`:

- `closed`
- `settling`
- `open`
- `low_confidence`
- `locked`
- `settled_win`
- `settled_loss`
- `nullified_wrong_team`
- `rejected_too_late`
- `rejected_market_closed`

### Semantics

- `closed`: backend does not currently have an honest market to offer
- `settling`: backend is resolving the previous play and should not accept new bets for this market window
- `open`: backend is actively offering a next-shot market bound to an assumed team
- `low_confidence`: backend's assumed team is degrading or stale; later runtime may still expose it for caution UI, but betting behavior must remain backend-controlled
- `locked`: bet was accepted against a backend-authored market binding and is awaiting the next shot
- `settled_win` / `settled_loss`: accepted bet resolved normally because actual next shot came from the assumed team
- `nullified_wrong_team`: accepted bet is refunded because the actual next shot came from a different team than the backend assumption
- `rejected_too_late`: bet was never accepted because it arrived after the relevant event/cutoff
- `rejected_market_closed`: bet was never accepted because there was no open backend market

## Assumed team identity

Defined in `game.AssumedPossessionState`:

- `assumedTeam`: team tricode only, e.g. `LAL`
- `confidence`: `high | medium | low | none`
- `reasoning`: short backend-authored explanation, e.g. `made basket by BOS -> inbound to LAL`
- `boundGameId`
- `boundRoundId`
- `resolvedEventId`
- `actualShotTeam` (only once known)

### Honesty rule

If ESPN data is ambiguous, `assumedTeam` should be absent and `marketState` should not pretend an open actionable market exists.

## Nullification vs too-late are distinct outcomes

This is the core integrity distinction.

### Nullification

- Resolution kind: `nullified_wrong_team`
- Meaning: the backend **accepted** a bet under an explicit assumed-team contract, but the actual next shot came from another team
- User impact: full refund
- Fields:
  - `contractResolution.kind = "nullified_wrong_team"`
  - `contractResolution.nullificationReason = "wrong_team_shot"`

### Too late

- Resolution kind: `rejected_too_late`
- Meaning: the backend **never accepted** the bet because the event/cutoff had already passed
- User impact: no charge
- Fields:
  - `contractResolution.kind = "rejected_too_late"`
  - `contractResolution.tooLateReason = "event_already_occurred" | "round_already_resolved" | "bet_timestamp_expired"`

### Precedence rule

Too-late is evaluated before wrong-team nullification. If the bet was never validly accepted, wrong-team semantics do not apply.

## Sim/live identifiers and lane isolation semantics

Defined in `game.LaneDescriptor`:

- `kind = "live" | "simulation"`
- `laneId`: stable lane identifier for caching, routing, and analytics
- `simulation`: boolean mirror for existing protocol compatibility
- `isolated`: must be `true` for simulation lane semantics

### Required rule

Frontend and downstream consumers must treat lane metadata as authoritative. They must not co-mingle live and simulation game state, market state, bet history, or settlement interpretation.

## Replay latency metadata

Defined in `game.ReplayLatencyMeta`.

These fields are reserved for later simulation work and make replay timing explicit:

- `replaySourceGameId`
- `replaySequence`
- `sourceEventTimestamp`
- `observedAt`
- `emittedAt`
- `feedLagMs`
- `replayOffsetMs`
- `synthetic`

These fields exist because simulation timing is not the same thing as live ESPN timing. Later work should expose both honestly.

## Bet contract surfaces

Two backend-owned structures define what a bet meant and how it resolved:

### `game.BetContractBinding`

Recorded at acceptance time.

- contract version
- `gameId`
- `roundId`
- `marketState`
- `assumedTeam`
- `confidence`
- `reasoning`
- `lane`

### `game.BetContractResolution`

Recorded at rejection/settlement time.

- contract version
- `kind`
- `actualShotTeam`
- `nullificationReason`
- `tooLateReason`
- `reasoning`

## Message surfaces reserved for Item 5 emission

The code now reserves these optional message fields so runtime implementation can populate them without reshaping the protocol later:

- `game.GameState.contractVersion`
- `game.GameState.assumedPossession`
- `ws.GameStateMessage.contractVersion`
- `bet.Bet.contract_binding`
- `bet.Bet.contract_resolution`
- `bet.SignInAckBetHistory.contractBinding`
- `bet.SignInAckBetHistory.contractResolution`
- `bet.BetAck.contractBinding`
- `bet.BetAck.contractResolution`
- `bet.BetResult.contractBinding`
- `bet.BetResult.contractResolution`

## Frontend dependency rule

Frontend item 5+ must consume backend-authored assumed-possession state directly.

Specifically, frontend must not:

- infer open/closed state from only ESPN scoreboard state
- infer assumed team from only `possession` or prior play text
- convert nullification into too-late or vice versa
- merge simulation and live semantics because the UI route happens to look similar

Instead, frontend should render the backend contract exactly as provided.

## What Item 5 still needs to do

Item 5 should implement actual production of these structures by:

1. deriving assumed-possession state from ESPN play outcomes
2. attaching `AssumedPossessionState` to outbound `game_state`
3. recording `BetContractBinding` when a bet is accepted
4. emitting `BetContractResolution` on rejection and settlement
5. populating replay latency metadata in simulation/replay paths
6. updating any downstream docs/examples once runtime emission is real

## Why this item matters

Before this change, the repo had live protocol docs and bet types, but no explicit backend-owned contract for assumed-team truth, confidence, outcome separation, or replay latency semantics. That left later items too much room to improvise. This foundation removes that ambiguity.
