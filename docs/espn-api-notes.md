# ESPN API Notes for NBA Shot Locations

## Endpoints used

### 1. Live game metadata / roster / status

`https://site.api.espn.com/apis/site/v2/sports/basketball/nba/summary?event={gameId}`

Used for:
- official game start time (`header.competitions[0].date`)
- game status (`header.competitions[0].status.type.*`)
- team abbreviations (`header.competitions[0].competitors[]`)
- player ID → display name mapping (`boxscore.players[].statistics[].athletes[]`)

### 2. Live play-by-play with shot coordinates

`https://sports.core.api.espn.com/v2/sports/basketball/leagues/nba/events/{gameId}/competitions/{gameId}/plays?limit=1000`

This is the key endpoint for Phase 1. Each play item includes fields such as:
- `id`
- `text`
- `wallclock`
- `shootingPlay`
- `scoringPlay`
- `pointsAttempted`
- `clock.displayValue`
- `clock.value`
- `period.number`
- `type.id`
- `type.text`
- `coordinate.x`
- `coordinate.y`
- `participants[]`
- sometimes `team.id` / `team.abbreviation`

### Other endpoints inspected during research

- `https://site.api.espn.com/apis/site/v2/sports/basketball/nba/scoreboard`
- `https://site.web.api.espn.com/apis/v2/scoreboard/header?sport=basketball&league=nba`
- `https://push.api.espn.com/apis/push/v2/scoreboard/header?sport=basketball&league=nba`
- `https://cdn.espn.com/core/nba/playbyplay?xhr=1&gameId={gameId}`

These were useful for confirming how ESPN organizes live game data, but the actual shot coordinates needed by the backend came directly from the core `plays` endpoint.

## Shot location format

### What field contains the location?

The shot location is in:
- `coordinate.x`
- `coordinate.y`

on each `shootingPlay: true` item in the core plays feed.

Example observed payload fragment:

```json
{
  "id": "4018109547",
  "text": "Jalen Suggs makes 21-foot jumper (Paolo Banchero assists)",
  "shootingPlay": true,
  "scoringPlay": true,
  "pointsAttempted": 2,
  "coordinate": {"x": 8, "y": 13},
  "clock": {"displayValue": "11:42", "value": 702},
  "period": {"number": 1},
  "type": {"id": "92", "text": "Jump Shot"},
  "wallclock": "2026-03-31T23:11:34Z"
}
```

## Coordinate system findings

This is the most important takeaway.

### It is **not** feet

The ESPN `coordinate` values are not raw feet or inches. They appear to be a normalized **offensive half-court grid** used by Gamecast.

### Observed scale from live game 401810954

From live/core play inspection:
- valid shot `x` values were roughly **1..49**
- valid shot `y` values were roughly **-1..30**
- rim / dunk / layup attempts clustered around **(25, 1..3)**
- corner threes clustered around **(2, 1..2)** and **(48, 1..2)**
- top-of-arc threes clustered around **y ≈ 24..30**

### Court mapping interpretation

Best-fit interpretation:
- origin is near the **left baseline corner** of the offensive half court
- `x` increases left → right across the court
- `y` increases baseline → half-court line
- the basket is near **(25, 1)**
- ESPN appears to normalize all shots into the **same offensive half-court orientation**, rather than storing full-court absolute coordinates

That means the frontend can render every shot on one shared half-court template without needing to mirror by possession direction.

## Gamecast / site behavior

The ESPN Gamecast page and CDN payloads expose the same play data shape, including `coordinate` and `hasShotChart`. The public website appears to use the same normalized coordinate system internally for rendering its shot chart.

The practical conclusion for this project is:
- use the core plays `coordinate` values directly
- treat them as half-court chart coordinates
- scale them onto a frontend court image rather than trying to reinterpret them as feet

## Coordinate quirks / gotchas

### 1. Free throws often carry sentinel invalid coordinates

Observed values included numbers like:
- `x = -214748340`
- `y = -214748365`

These are clearly invalid chart positions and appear to be sentinel placeholders rather than real location data.

The backend preserves the raw values in `location_x` / `location_y` and marks the shot as `shot_type=free_throw` and `location_zone=free_throw` or `invalid` as appropriate.

### 2. `wallclock` is the useful ESPN timestamp

For real-time replay and latency compensation, the most useful ESPN-side timestamp is:
- `wallclock`

The backend stores it as `espn_timestamp`.

### 3. `clock.displayValue` must be preserved

`clock.displayValue` plus `period.number` is important for frontend playback because users think in terms of quarter + game clock, not just UTC timestamps.

### 4. Player names are sometimes easier to resolve from `participants`

The best source order is:
1. shooter in `participants[]`
2. player ID → name map from summary boxscore
3. fallback parse from the natural-language `text`

### 5. The feed is full-state, not incremental

Polling the core plays endpoint returns the full list of plays seen so far. Deduplication must be done client-side with ESPN play IDs.

## Persistence notes used by this backend

The backend writes:

```json
{
  "game_id": "401810954",
  "game_start_time": "2026-03-31T23:00Z",
  "last_updated": "2026-03-31T23:59:59.123456789Z",
  "location_schema": {
    "description": "ESPN normalized offensive half-court coordinates",
    "x_range": [1, 49],
    "y_range": [-1, 30],
    "origin": "baseline-left corner of normalized offensive half court",
    "units": "normalized court grid units (not feet)",
    "notes": "basket is roughly (25,1); free throws may use sentinel invalid coordinates"
  },
  "shots": []
}
```

## Smoke-test notes

The service was tested against game `401810954` by polling the live/finalized feeds and writing `shots_401810954.json`. Example console lines:

```text
[Q1 11:42] Jalen Suggs (ORL) MADE 2PT @ (8.0, 13.0) — shots stored: 1
[Q1 9:20] Collin Gillespie (PHX) MISSED 3PT @ (7.0, 24.0) — shots stored: 10
[Q1 9:36] Wendell Carter Jr. (ORL) MADE FT @ (-214748340.0, -214748365.0) — shots stored: 9
```

If the game is already final when the process starts, the backend still backfills the full shot history from the endpoint and then idles cleanly on subsequent polls.
