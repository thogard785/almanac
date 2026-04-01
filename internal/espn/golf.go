package espn

import (
	"context"
	"fmt"

	"github.com/almanac/espn-shots/internal/game"
)

const (
	GolfScoreboardURL = "https://site.api.espn.com/apis/site/v2/sports/golf/pga/scoreboard"
	GolfPlaysURLFmt   = "https://sports.core.api.espn.com/v2/sports/golf/leagues/pga/events/%s/competitions/%s/plays?limit=1000"
)

type golfPlaysResponse struct {
	Items []golfPlay `json:"items"`
}

type golfPlay struct {
	ID             string `json:"id"`
	SequenceNumber string `json:"sequenceNumber"`
	Text           string `json:"text"`
	Wallclock      string `json:"wallclock"`
	Period         struct {
		Number int `json:"number"`
	} `json:"period"`
	Coordinate *struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"coordinate"`
	Participants []struct {
		Athlete struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"athlete"`
	} `json:"participants"`
	Shot *struct {
		ID     string `json:"id"`
		Number int    `json:"number"`
	} `json:"shot"`
	Round *struct {
		Number int `json:"number"`
	} `json:"round"`
}

func FetchGolfScoreboard(ctx context.Context, client *Client) ([]string, error) {
	resp, err := FetchJSON[scoreboardResponse](ctx, client, GolfScoreboardURL)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, ev := range resp.Events {
		state := ev.Status.Type.State
		if state == "in" || state == "post" {
			ids = append(ids, ev.ID)
		}
	}
	return ids, nil
}

func PollGolfGame(ctx context.Context, client *Client, gameID string, seen map[string]struct{}) (game.GameState, []game.PlayEvent, error) {
	board, err := FetchJSON[scoreboardResponse](ctx, client, GolfScoreboardURL)
	if err != nil {
		return game.GameState{}, nil, err
	}
	state := normalizeGolfGameState(gameID, board)
	plays, err := FetchJSON[golfPlaysResponse](ctx, client, fmt.Sprintf(GolfPlaysURLFmt, gameID, gameID))
	if err != nil {
		return state, nil, nil
	}
	var events []game.PlayEvent
	for _, play := range plays.Items {
		playID := golfPlayID(play)
		if playID == "" {
			continue
		}
		if _, ok := seen[playID]; ok {
			continue
		}
		seen[playID] = struct{}{}
		events = append(events, normalizeGolfPlay(gameID, playID, play))
	}
	return state, events, nil
}

func normalizeGolfGameState(gameID string, board scoreboardResponse) game.GameState {
	state := game.GameState{GameID: gameID, Sport: string(game.SportGolf), Status: "in_progress", State: "in", Tracked: true}
	for _, ev := range board.Events {
		if ev.ID != gameID {
			continue
		}
		state.Status = normalizeStatus(ev.Status.Type.State, ev.Status.Type.Completed)
		state.State = ev.Status.Type.State
		state.Detail = ev.Status.Type.Detail
		state.StartTime = ev.Date
		state.Completed = ev.Status.Type.Completed
		state.Home = ev.ShortName
		state.Period = stringsTrimSpaceOr(ev.Status.Type.Description, ev.Status.Type.Detail)
		state.Clock = ""
		return state
	}
	return state
}

func normalizeGolfPlay(gameID, playID string, play golfPlay) game.PlayEvent {
	playerName := ""
	playerID := ""
	if len(play.Participants) > 0 {
		playerID = play.Participants[0].Athlete.ID
		playerName = play.Participants[0].Athlete.DisplayName
	}
	data := map[string]any{
		"player_id":   playerID,
		"player_name": playerName,
		"hole":        play.Period.Number,
		"round":       golfRound(play),
		"shot_number": golfShotNumber(play),
		"description": play.Text,
	}
	return game.PlayEvent{
		GameID:    gameID,
		PlayID:    playID,
		Sport:     string(game.SportGolf),
		Timestamp: game.ParseESPNTime(play.Wallclock),
		Location:  normalizeGolfCoordFromPtr(play.Coordinate),
		EventData: data,
	}
}

func normalizeGolfCoordFromPtr(coord *struct {
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
		y *= 7
	case x >= 0 && x <= 500 && y >= 0 && y <= 700:
		// already normalized
	default:
		x = clamp(x, 0, 500)
		y = clamp(y, 0, 700)
	}
	return &game.Coord{X: round2(x), Y: round2(y)}
}

func golfPlayID(play golfPlay) string {
	if play.Shot != nil && play.Shot.ID != "" {
		return play.Shot.ID
	}
	if play.ID != "" {
		return play.ID
	}
	return fmt.Sprintf("r%d-h%d-s%d", golfRound(play), play.Period.Number, golfShotNumber(play))
}

func golfRound(play golfPlay) int {
	if play.Round != nil {
		return play.Round.Number
	}
	return 0
}

func golfShotNumber(play golfPlay) int {
	if play.Shot != nil {
		return play.Shot.Number
	}
	return 0
}

func stringsTrimSpaceOr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
