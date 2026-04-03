# Almanac Backend

Go backend for the Almanac spatial micro-betting product. Polls ESPN live feeds for NBA, MLB, and golf, manages game state, processes EIP-712 signed bets over WebSocket, and runs an isolated simulation mode with replay capability.

## What it does

- **ESPN polling:** Discovers live/recent games from ESPN scoreboard APIs (NBA, MLB), polls play-by-play for shot/pitch coordinates
- **WebSocket server:** Serves real-time game state, play events, bet acknowledgments, and bet results to connected frontends
- **Bet engine:** Accepts EIP-712 signed bets, validates signatures, manages per-wallet balances and nonces, resolves bets against incoming play coordinates using radius-based hit detection
- **Simulation mode:** Isolated sim lane with $100 virtual balance per wallet, delayed mirror of live ESPN games plus archived completed-game replay assets, separate WebSocket fanout (no state contagion with real-money lane)
- **Protocol:** v3 — `signin`/`signin_ack`/`place_bet`/`bet_ack`/`bet_result` with Monad mainnet chain ID 143
- **Assumed-possession contract foundation:** `internal/game/assumed_possession.go` and `docs/backend-contract-foundation.md` define the backend-authored ESPN-only truth model that later frontend/live/simulation work must consume directly

## Build & run

```bash
cd /data/github/almanac
/usr/local/go/bin/go build -o almanac .
```

The service runs as a systemd user service on port 8090:

```bash
systemctl --user start almanac
systemctl --user status almanac --no-pager
curl -s http://localhost:8090/health   # returns "ok"
```

## Key docs

- `DATA_FLOW.md` — canonical WebSocket protocol spec (keep in sync with frontend copy)
- `docs/backend-contract-foundation.md` — explicit assumed-possession backend contract, outcome semantics, and Item 5 implementation boundary
- `docs/simulation-mode.md` — simulation lane architecture
- `docs/espn-api-notes.md` — ESPN endpoint details and coordinate system

## Coordinate system

ESPN half-court grid: X 0–50 (left→right), Y 0–30 (baseline→halfcourt). Basket at ~(25, 1). Free throws use sentinel values normalized to x=0, y=0, zone=`free_throw`.

See `docs/espn-api-notes.md` for full details.
