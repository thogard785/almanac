package espn

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/almanac/espn-shots/internal/game"
)

const (
	MLBScoreboardURL = "https://site.api.espn.com/apis/site/v2/sports/baseball/mlb/scoreboard"
	MLBSummaryURLFmt = "https://site.api.espn.com/apis/site/v2/sports/baseball/mlb/summary?event=%s"
	MLBPlaysURLFmt   = "https://sports.core.api.espn.com/v2/sports/baseball/leagues/mlb/events/%s/competitions/%s/plays?limit=1000"
)

type mlbSummaryResponse struct {
	Header struct {
		Competitions []struct {
			Date   string `json:"date"`
			Status struct {
				Type struct {
					State     string `json:"state"`
					Completed bool   `json:"completed"`
					Detail    string `json:"detail"`
				} `json:"type"`
				Period int `json:"period"`
			} `json:"status"`
			Competitors []struct {
				HomeAway string `json:"homeAway"`
				Score    string `json:"score"`
				Team     struct {
					Abbreviation string `json:"abbreviation"`
				} `json:"team"`
			} `json:"competitors"`
		} `json:"competitions"`
	} `json:"header"`
}

type mlbPlaysResponse struct {
	Items []mlbPlay `json:"items"`
}

type mlbPlay struct {
	ID               string  `json:"id"`
	SequenceNumber   string  `json:"sequenceNumber"`
	Text             string  `json:"text"`
	Wallclock        string  `json:"wallclock"`
	AtBatID          string  `json:"atBatId"`
	AtBatPitchNumber int     `json:"atBatPitchNumber"`
	PitchVelocity    float64 `json:"pitchVelocity"`
	Period           struct {
		Type   string `json:"type"`
		Number int    `json:"number"`
	} `json:"period"`
	Type struct {
		Type         string `json:"type"`
		Text         string `json:"text"`
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
		Text         string `json:"text"`
		Abbreviation string `json:"abbreviation"`
	} `json:"pitchType"`
	PitchCount *struct {
		Balls   int `json:"balls"`
		Strikes int `json:"strikes"`
	} `json:"pitchCount"`
	ResultCount *struct {
		Balls   int `json:"balls"`
		Strikes int `json:"strikes"`
	} `json:"resultCount"`
	Outs         int `json:"outs"`
	Participants []struct {
		Type    string `json:"type"`
		Athlete struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"athlete"`
	} `json:"participants"`
}

func FetchMLBScoreboard(ctx context.Context, client *Client) ([]string, error) {
	events, err := fetchScoreboardEvents(ctx, client, MLBScoreboardURL)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, ev := range events {
		state := ev.Status.Type.State
		if state == "in" || state == "post" {
			ids = append(ids, ev.ID)
		}
	}
	return ids, nil
}

func PollMLBGame(ctx context.Context, client *Client, gameID string, seen map[string]struct{}) (game.GameState, []game.PlayEvent, error) {
	summary, err := FetchJSON[mlbSummaryResponse](ctx, client, fmt.Sprintf(MLBSummaryURLFmt, gameID))
	if err != nil {
		return game.GameState{}, nil, err
	}
	plays, err := FetchJSON[mlbPlaysResponse](ctx, client, fmt.Sprintf(MLBPlaysURLFmt, gameID, gameID))
	if err != nil {
		return normalizeMLBGameState(gameID, summary), nil, err
	}
	state := normalizeMLBGameState(gameID, summary)
	ordered := append([]mlbPlay(nil), plays.Items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return sequenceKey(ordered[i].SequenceNumber, ordered[i].ID) < sequenceKey(ordered[j].SequenceNumber, ordered[j].ID)
	})
	var events []game.PlayEvent
	for _, play := range ordered {
		id := play.ID
		if id == "" {
			id = play.SequenceNumber
		}
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		if play.PitchCoordinate == nil && play.HitCoordinate == nil {
			continue
		}
		seen[id] = struct{}{}
		events = append(events, normalizeMLBPlay(gameID, id, play))
	}
	return state, events, nil
}

func normalizeMLBGameState(gameID string, summary mlbSummaryResponse) game.GameState {
	state := game.GameState{GameID: gameID, Sport: string(game.SportMLB), Status: "in_progress", State: "in", Tracked: true}
	if len(summary.Header.Competitions) == 0 {
		return state
	}
	comp := summary.Header.Competitions[0]
	state.Status = normalizeStatus(comp.Status.Type.State, comp.Status.Type.Completed)
	state.State = comp.Status.Type.State
	state.Detail = comp.Status.Type.Detail
	state.StartTime = comp.Date
	state.Completed = comp.Status.Type.Completed
	state.Period = formatMLBPeriod(comp.Status.Period, comp.Status.Type.Detail)
	for _, competitor := range comp.Competitors {
		if competitor.HomeAway == "home" {
			state.Home = competitor.Team.Abbreviation
			state.HomeScore = game.ParseScoreInt(competitor.Score)
		} else {
			state.Away = competitor.Team.Abbreviation
			state.AwayScore = game.ParseScoreInt(competitor.Score)
		}
	}
	return state
}

func normalizeMLBPlay(gameID, playID string, play mlbPlay) game.PlayEvent {
	location, eventType := normalizeMLBLocation(play)
	pitcher, batter := "", ""
	for _, participant := range play.Participants {
		switch participant.Type {
		case "pitcher":
			pitcher = participant.Athlete.DisplayName
		case "batter":
			batter = participant.Athlete.DisplayName
		}
	}
	data := map[string]any{
		"event_type":     eventType,
		"description":    play.Text,
		"inning":         play.Period.Number,
		"inning_half":    play.Period.Type,
		"pitch_type":     pickPitchType(play),
		"pitch_velocity": play.PitchVelocity,
		"pitch_number":   play.AtBatPitchNumber,
		"pitcher_name":   pitcher,
		"batter_name":    batter,
		"outs":           play.Outs,
		"pitch_location": normalizeMLBPitchCoordFromPtr(play.PitchCoordinate),
		"hit_location":   normalizeMLBHitCoordFromPtr(play.HitCoordinate),
	}
	return game.PlayEvent{
		GameID:    gameID,
		PlayID:    playID,
		Sport:     string(game.SportMLB),
		Timestamp: game.ParseESPNTime(play.Wallclock),
		Location:  location,
		EventData: data,
	}
}

func normalizeMLBLocation(play mlbPlay) (*game.Coord, string) {
	if play.HitCoordinate != nil {
		return normalizeMLBHitCoordFromPtr(play.HitCoordinate), "hit_ball"
	}
	if play.PitchCoordinate != nil {
		return normalizeMLBPitchCoordFromPtr(play.PitchCoordinate), "pitch"
	}
	return nil, "pitch"
}

func normalizeMLBPitchCoordFromPtr(coord *struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}) *game.Coord {
	if coord == nil {
		return nil
	}
	const (
		rawXMin = 85.0
		rawXMax = 145.0
		rawYMin = 145.0
		rawYMax = 205.0
	)
	x := 150 + ((coord.X-rawXMin)/(rawXMax-rawXMin))*200
	y := 100 + ((coord.Y-rawYMin)/(rawYMax-rawYMin))*300
	return &game.Coord{X: round2(clamp(x, 150, 350)), Y: round2(clamp(y, 100, 400))}
}

func normalizeMLBHitCoordFromPtr(coord *struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}) *game.Coord {
	if coord == nil {
		return nil
	}
	x, y := coord.X, coord.Y
	switch {
	case x >= 0 && x <= 100 && y >= 0 && y <= 100:
		x *= 5
		y *= 5
	case x >= -250 && x <= 250 && y >= -250 && y <= 250:
		x = x + 250
		y = 250 - y
	}
	return &game.Coord{X: round2(clamp(x, 0, 500)), Y: round2(clamp(y, 0, 500))}
}

func pickPitchType(play mlbPlay) string {
	if play.PitchType != nil && play.PitchType.Text != "" {
		return play.PitchType.Text
	}
	if play.Type.Text != "" {
		return play.Type.Text
	}
	return play.Type.Type
}

func formatMLBPeriod(period int, detail string) string {
	if period > 0 {
		upper := strings.ToUpper(detail)
		switch {
		case strings.Contains(upper, "TOP"):
			return fmt.Sprintf("Top %d", period)
		case strings.Contains(upper, "BOT") || strings.Contains(upper, "BOTTOM"):
			return fmt.Sprintf("Bottom %d", period)
		}
		return fmt.Sprintf("Inning %d", period)
	}
	return ""
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
