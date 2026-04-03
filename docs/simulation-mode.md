# Simulation mode backend architecture

## Isolation model

Simulation mode is now implemented as an **isolated delayed mirror of live ESPN games**, not as a view onto live-money runtime state.

- **Identity split:** the same wallet can exist in both lanes, but mode is bound per websocket connection after a signed `signin` carrying `simulation=true|false`.
- **Balance split:**
  - regular balances remain in `internal/bet`'s default balance provider
  - simulation balances live in `internal/sim/SimBalanceProvider`
  - simulation wallets are initialized to **$100** on first simulation sign-in only
- **Bet split:**
  - regular bets persist under `data/bets`
  - simulation bets persist under `data/sim_bets`
  - each lane has its own `bet.Engine`, nonce tracking, pending bets, results, and balance updates
- **Round/resolution split:**
  - live assumed-possession tracking remains on the live lane only
  - simulation has its own `game.AssumedPossessionTracker`, fed only by delayed mirrored events
  - simulation bet bindings and resolutions carry `lane.kind = "simulation"` and `lane.isolated = true`
- **Game-state split:**
  - simulation games are duplicated from live games with `game_id = sim:<liveGameId>`
  - shell game rows may appear before delayed events release, but live score/clock/possession are withheld until the delayed event for that state is emitted
  - simulation `game_state` never reuses live in-memory round state directly
- **Replay timing split:**
  - every mirrored live event is queued behind a backend delay window (`internal/sim.DefaultEventDelay`, currently 15s)
  - when the delayed event releases, the sim lane emits a namespaced play and updates the isolated sim game snapshot
  - `assumedPossession.replayLatency` carries `replaySourceGameId`, `replaySequence`, `sourceEventTimestamp`, `observedAt`, `emittedAt`, `feedLagMs`, and `replayOffsetMs`
- **Websocket fanout split:**
  - regular broadcasts go only to regular connections
  - simulation broadcasts go only to simulation connections
  - wallet-targeted result/balance messages are filtered by both wallet and mode

## Runtime flow

1. Live ESPN polling continues to drive the real-money lane as before.
2. The simulation manager receives the same live game roster, but only as **source input** for building isolated sim-lane shells.
3. Each live play event is captured with the matching live snapshot and queued for delayed release.
4. Once the configured delay elapses, the backend emits a simulation-namespaced play event and advances only the simulation lane's round tracker, game state, and bet engine.
5. Simulation bet admission reuses the assumed-possession contract semantics from live money, but only against the delayed simulation round/state.
6. Live and simulation balances, ledgers, round IDs, contract bindings, and settlement payloads remain lane-scoped.

## What item 8 changed

- Simulation no longer depends on a single completed-game replayer for active play.
- Simulation bets can now bind against **live real-game data** without mutating live-money state.
- Simulation acknowledgements and results now use the same assumed-possession contract family as live money, but with simulation lane metadata.
- Replay/delay mechanics are explicit instead of implicit: the backend now says both **what live event this came from** and **how delayed the sim lane is**.

## Frontend contract for item 9

Frontend still does **not** need a separate message family. It does need to treat the following as authoritative:

- `signin.simulation` selects the lane
- `place_bet.simulation` must match the signed connection lane
- `game_state.simulation`, `game_state.games[*].simulation`, and `play_event.simulation`
- simulation-namespaced `gameId` / `roundId` / `playId`
- `assumedPossession.lane`
- `assumedPossession.replayLatency`
- simulation bet `contractBinding` / `contractResolution`

Frontend must **not**:

- combine live and simulation histories because the underlying source game is the same
- assume a live `gameId` and a sim `gameId` can share cache keys
- infer replay delay from local timers when backend latency metadata is present
- reinterpret simulation round IDs as live round IDs

## Storage additions

- `data/sim_bets/` — persisted simulation bets/results
- `data/sim_games/` — archived completed NBA games still saved for future tooling/replay work

Completed-game archives remain useful, but item 8's runtime simulation path is now the delayed live mirror described above.
