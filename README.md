# Almanac ESPN Shot Backend

Phase 1 Go backend for polling ESPN's live NBA play-by-play feeds and persisting shot locations to JSON.

## Usage

```bash
GAME_ID=401810954 /usr/local/go/bin/go run .
# or
/usr/local/go/bin/go run . --game-id 401810954 --poll-interval 3s
```

Output is written to `shots_{gameId}.json` by default.

See `docs/espn-api-notes.md` for endpoint and coordinate-format notes.
