# Simulation mode backend architecture

## Isolation model

Simulation mode is implemented as a separate backend lane that reuses existing message shapes but not existing state.

- **Identity split:** the same wallet can exist in both lanes, but mode is bound per websocket connection after a signed `signin` carrying `simulation=true|false`.
- **Balance split:**
  - regular balances remain in `internal/bet`'s default balance provider
  - simulation balances live in `internal/sim/SimBalanceProvider`
  - simulation wallets are initialized to **$100** on first simulation sign-in only
- **Bet split:**
  - regular bets persist under `data/bets`
  - simulation bets persist under `data/sim_bets`
  - each lane has its own `bet.Engine`, nonce tracking, pending bets, results, and balance updates
- **Game/stream split:**
  - regular games come from the live ESPN manager
  - completed NBA games are saved for replay under `data/sim_games`
  - simulation uses a single shared `sim.Manager` + `Replayer` that replays the most recently saved completed NBA game as a synthetic live game with `game_id` namespaced as `sim:<original>`
- **Websocket fanout split:**
  - regular broadcasts go only to regular connections
  - simulation broadcasts go only to simulation connections
  - wallet-targeted result/balance messages are filtered by both wallet and mode

## Lifecycle

1. Client signs in with `simulation=false` (default regular lane) or `simulation=true` (simulation lane).
2. On first simulation sign-in for a wallet, backend creates the isolated sim wallet state with `$100`.
3. If no simulation replay is active, backend starts one from the newest saved completed NBA game.
4. Simulation clients receive only simulation `game_state` / `play_event` messages, all marked with `simulation=true` where relevant.
5. To return to regular mode, client must send a new signed `signin` with `simulation=false`.

## Frontend hooks needed later

Frontend does **not** need new message families. It does need to:

- send signed `signin` with `simulation=true` when entering simulation mode
- send signed `signin` with `simulation=false` to switch back to regular mode
- include `simulation=true` on simulation `place_bet` messages
- treat `game_state.games[*].simulation`, top-level `game_state.simulation`, and `play_event.simulation` as authoritative lane markers
- render only the lane that matches the active signed connection

## Storage additions

- `data/sim_bets/` — persisted simulation bets/results
- `data/sim_games/` — persisted completed NBA games available for replay

These additions are intentionally narrow and mode-scoped to keep reasoning about contamination simple.
