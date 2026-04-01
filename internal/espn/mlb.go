package espn

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// MLB API URLs
const (
	MLBScoreboardURL = "https://site.api.espn.com/apis/site/v2/sports/baseball/mlb/scoreboard"
	MLBSummaryURLFmt = "https://site.api.espn.com/apis/site/v2/sports/baseball/mlb/summary?event=%s"
	MLBPlaysURLFmt   = "https://sports.core.api.espn.com/v2/sports/baseball/leagues/mlb/events/%s/competitions/%s/plays?limit=1000"
)

// --- MLB Play types ---

type MLBPlaysResponse struct {
	Items []MLBPlayItem `json:"items"`
	Count int           `json:"count"`
}

type MLBPlayItem struct {
	ID             string `json:"id"`
	SequenceNumber string `json:"sequenceNumber"`
	Text           string `json:"text"`
	Wallclock      string `json:"wallclock"`
	ScoringPlay    bool   `json:"scoringPlay"`
	AtBatId        string `json:"atBatId"`
	Period         struct {
		Type         string `json:"type"` // "Top" or "Bottom"
		Number       int    `json:"number"`
		DisplayValue string `json:"displayValue"`
	} `json:"period"`
	Type struct {
		ID           string `json:"id"`
		Text         string `json:"text"`
		Type         string `json:"type"`
		Abbreviation string `json:"abbreviation"`
	} `json:"type"`
	PitchCoordinate *struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"pitchCoordinate"`
	HitCoordinate *struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"hitCoordinate"`
	PitchType *struct {
		ID           string `json:"id"`
		Text         string `json:"text"`
		Abbreviation string `json:"abbreviation"`
	} `json:"pitchType"`
	PitchVelocity    float64 `json:"pitchVelocity"`
	AtBatPitchNumber int     `json:"atBatPitchNumber"`
	PitchCount       *struct {
		Balls   int `json:"balls"`
		Strikes int `json:"strikes"`
	} `json:"pitchCount"`
	ResultCount *struct {
		Balls   int `json:"balls"`
		Strikes int `json:"strikes"`
	} `json:"resultCount"`
	Outs         int  `json:"outs"`
	Valid        bool `json:"valid"`
	Participants []struct {
		Type    string `json:"type"`
		Athlete struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
			Ref         string `json:"$ref"`
		} `json:"athlete"`
	} `json:"participants"`
}

// PitchEvent represents a single pitch in an MLB game.
type PitchEvent struct {
	EventID        string  `json:"event_id"`
	GameID         string  `json:"game_id"`
	TimestampNS    int64   `json:"timestamp_ns"`
	ESPNTimestamp  string  `json:"espn_timestamp"`
	Inning         int     `json:"inning"`
	InningHalf     string  `json:"inning_half"` // "Top" or "Bottom"
	PitcherID      string  `json:"pitcher_id"`
	PitcherName    string  `json:"pitcher_name"`
	BatterID       string  `json:"batter_id"`
	BatterName     string  `json:"batter_name"`
	PitchType      string  `json:"pitch_type"`
	PitchVelocity  float64 `json:"pitch_velocity"`
	PitchNumber    int     `json:"pitch_number"`
	ResultType     string  `json:"result_type"` // e.g. "strike-looking", "ball", "foul", "hit-into-play"
	PitchX         float64 `json:"pitch_x"`
	PitchY         float64 `json:"pitch_y"`
	HitX           float64 `json:"hit_x"`
	HitY           float64 `json:"hit_y"`
	HasPitchCoords bool    `json:"has_pitch_coords"`
	HasHitCoords   bool    `json:"has_hit_coords"`
	Description    string  `json:"description"`
	RawPayload     string  `json:"raw_payload"`
	// Zone is derived from pitch coordinates for the 1-9 strike zone grid
	Zone string `json:"zone"`
}

// MLBParser handles MLB-specific ESPN data.
type MLBParser struct {
	client *Client
}

func NewMLBParser(client *Client) *MLBParser {
	return &MLBParser{client: client}
}

// FetchScoreboard returns all games on today's MLB scoreboard.
func (p *MLBParser) FetchScoreboard(ctx context.Context) ([]ScoreboardEvent, error) {
	resp, err := FetchJSON[ScoreboardResponse](ctx, p.client, MLBScoreboardURL)
	if err != nil {
		return nil, err
	}
	return resp.Events, nil
}

// FetchGameInfo returns metadata for an MLB game.
func (p *MLBParser) FetchGameInfo(ctx context.Context, gameID string) (*GameInfo, error) {
	summary, err := FetchJSON[SummaryResponse](ctx, p.client, fmt.Sprintf(MLBSummaryURLFmt, gameID))
	if err != nil {
		return nil, err
	}
	if len(summary.Header.Competitions) == 0 {
		return nil, fmt.Errorf("no competitions in summary for %s", gameID)
	}
	comp := summary.Header.Competitions[0]
	info := &GameInfo{
		GameID:    gameID,
		StartTime: comp.Date,
		Status:    comp.Status.Type.Name,
		State:     comp.Status.Type.State,
		Detail:    comp.Status.Type.Detail,
		Completed: comp.Status.Type.Completed,
	}
	for _, c := range comp.Competitors {
		if c.HomeAway == "home" {
			info.HomeTeam = c.Team.Abbreviation
			info.HomeScore = c.Score
		} else {
			info.AwayTeam = c.Team.Abbreviation
			info.AwayScore = c.Score
		}
	}
	return info, nil
}

// FetchPitches returns new pitch events for a game.
func (p *MLBParser) FetchPitches(ctx context.Context, gameID string, seen map[string]struct{}) ([]PitchEvent, *GameInfo, error) {
	info, err := p.FetchGameInfo(ctx, gameID)
	if err != nil {
		return nil, nil, err
	}

	plays, err := FetchJSON[MLBPlaysResponse](ctx, p.client, fmt.Sprintf(MLBPlaysURLFmt, gameID, gameID))
	if err != nil {
		return nil, info, fmt.Errorf("fetch plays: %w", err)
	}

	// Sort by sequence
	ordered := make([]MLBPlayItem, len(plays.Items))
	copy(ordered, plays.Items)
	sort.SliceStable(ordered, func(i, j int) bool {
		return mlbSortKey(ordered[i]) < mlbSortKey(ordered[j])
	})

	var pitches []PitchEvent
	for _, play := range ordered {
		if play.ID == "" {
			continue
		}
		// Only include plays that have pitch data (pitch coordinate or pitch type)
		if play.PitchCoordinate == nil && play.PitchType == nil {
			continue
		}
		if _, exists := seen[play.ID]; exists {
			continue
		}
		pitch := convertMLBPlay(gameID, play)
		pitches = append(pitches, pitch)
	}
	return pitches, info, nil
}

func convertMLBPlay(gameID string, play MLBPlayItem) PitchEvent {
	ev := PitchEvent{
		EventID:       play.ID,
		GameID:        gameID,
		ESPNTimestamp: play.Wallclock,
		Inning:        play.Period.Number,
		InningHalf:    play.Period.Type,
		PitchNumber:   play.AtBatPitchNumber,
		ResultType:    play.Type.Type,
		Description:   play.Text,
		RawPayload:    marshalMLBPlay(play),
	}

	if play.PitchType != nil {
		ev.PitchType = play.PitchType.Text
	}
	ev.PitchVelocity = play.PitchVelocity

	if play.PitchCoordinate != nil {
		ev.PitchX = play.PitchCoordinate.X
		ev.PitchY = play.PitchCoordinate.Y
		ev.HasPitchCoords = true
		ev.Zone = inferPitchZone(play.PitchCoordinate.X, play.PitchCoordinate.Y)
	}
	if play.HitCoordinate != nil {
		ev.HitX = play.HitCoordinate.X
		ev.HitY = play.HitCoordinate.Y
		ev.HasHitCoords = true
	}

	// Extract pitcher and batter from participants
	for _, p := range play.Participants {
		id := p.Athlete.ID
		if id == "" {
			id = athleteIDFromRef(p.Athlete.Ref)
		}
		name := p.Athlete.DisplayName
		switch p.Type {
		case "pitcher":
			ev.PitcherID = id
			ev.PitcherName = name
		case "batter":
			ev.BatterID = id
			ev.BatterName = name
		}
	}

	return ev
}

// inferPitchZone maps pitch coordinates to a 1-9 zone grid.
// ESPN pitch coordinates observed: x roughly 70-160, y roughly 130-210.
// The strike zone is roughly centered around x=115, y=170.
// Zone grid (batter's perspective):
//
//	1 | 2 | 3
//	---------
//	4 | 5 | 6
//	---------
//	7 | 8 | 9
func inferPitchZone(x, y float64) string {
	// Approximate strike zone bounds from observed data
	const (
		xMin = 85.0
		xMax = 145.0
		yMin = 145.0
		yMax = 205.0
	)

	// Outside the zone
	if x < xMin || x > xMax || y < yMin || y > yMax {
		return "outside"
	}

	xThird := (xMax - xMin) / 3.0
	yThird := (yMax - yMin) / 3.0

	col := int(math.Floor((x - xMin) / xThird))
	row := int(math.Floor((y - yMin) / yThird))
	if col > 2 {
		col = 2
	}
	if row > 2 {
		row = 2
	}

	zone := row*3 + col + 1
	return strconv.Itoa(zone)
}

// MLBWinCheck checks if a bet wins against an actual pitch event.
// For zone bets: exact zone match. For coordinate bets: within radius.
func MLBWinCheck(predictedX, predictedY float64, predictedZone string, actual PitchEvent, radius float64) (won bool, dist float64) {
	// Zone-based matching
	if predictedZone != "" && actual.Zone != "" {
		return predictedZone == actual.Zone, 0
	}
	// Coordinate-based matching
	if actual.HasPitchCoords {
		dx := predictedX - actual.PitchX
		dy := predictedY - actual.PitchY
		dist = math.Sqrt(dx*dx + dy*dy)
		return dist <= radius, dist
	}
	return false, 0
}

func mlbSortKey(play MLBPlayItem) int {
	if play.SequenceNumber != "" {
		if n, err := strconv.Atoi(play.SequenceNumber); err == nil {
			return n
		}
	}
	if n, err := strconv.Atoi(play.ID); err == nil {
		return n
	}
	return 0
}

func marshalMLBPlay(play MLBPlayItem) string {
	payload, err := json.Marshal(play)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

// isPitchPlay returns true if the play type is a pitch-related event.
func isPitchPlay(playType string) bool {
	pitchTypes := []string{
		"strike-looking", "strike-swinging", "foul", "ball",
		"hit-into-play", "foul-tip", "hit-by-pitch",
	}
	t := strings.ToLower(playType)
	for _, pt := range pitchTypes {
		if t == pt {
			return true
		}
	}
	return false
}
