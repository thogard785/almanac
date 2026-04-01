package espn

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/almanac/espn-shots/internal/game"
)

const (
	NBAScoreboardURL = "https://site.api.espn.com/apis/site/v2/sports/basketball/nba/scoreboard"
	NBASummaryURLFmt = "https://site.api.espn.com/apis/site/v2/sports/basketball/nba/summary?event=%s"
	NBAPlaysURLFmt   = "https://sports.core.api.espn.com/v2/sports/basketball/leagues/nba/events/%s/competitions/%s/plays?limit=1000"
)

type scoreboardResponse struct {
	Events []scoreboardEvent `json:"events"`
}

type scoreboardEvent struct {
	ID           string                  `json:"id"`
	Date         string                  `json:"date"`
	Name         string                  `json:"name"`
	ShortName    string                  `json:"shortName"`
	Competitions []scoreboardCompetition `json:"competitions"`
	Status       scoreboardStatus        `json:"status"`
}

type scoreboardCompetition struct {
	Competitors []scoreboardCompetitor `json:"competitors"`
	Status      scoreboardStatus       `json:"status"`
}

type scoreboardCompetitor struct {
	HomeAway string `json:"homeAway"`
	Score    string `json:"score"`
	Team     struct {
		Abbreviation string `json:"abbreviation"`
	} `json:"team"`
}

type scoreboardStatus struct {
	Type scoreboardStatusType `json:"type"`
}

type scoreboardStatusType struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	Detail      string `json:"detail"`
	Description string `json:"description"`
	Completed   bool   `json:"completed"`
}

type nbaSummaryResponse struct {
	Header struct {
		Competitions []struct {
			Date   string `json:"date"`
			Status struct {
				Type struct {
					Name        string `json:"name"`
					State       string `json:"state"`
					Detail      string `json:"detail"`
					Description string `json:"description"`
					Completed   bool   `json:"completed"`
				} `json:"type"`
				Period int `json:"period"`
			} `json:"status"`
			Competitors []struct {
				HomeAway string `json:"homeAway"`
				Score    string `json:"score"`
				Team     struct {
					ID           string `json:"id"`
					Abbreviation string `json:"abbreviation"`
				} `json:"team"`
			} `json:"competitors"`
			Situation *struct {
				Possession string `json:"possession"`
			} `json:"situation"`
		} `json:"competitions"`
	} `json:"header"`
	Boxscore struct {
		Players []struct {
			Team struct {
				Abbreviation string `json:"abbreviation"`
			} `json:"team"`
			Statistics []struct {
				Athletes []struct {
					Athlete struct {
						ID          string `json:"id"`
						DisplayName string `json:"displayName"`
					} `json:"athlete"`
				} `json:"athletes"`
			} `json:"statistics"`
		} `json:"players"`
	} `json:"boxscore"`
}

type nbaPlaysResponse struct {
	Items []nbaPlay `json:"items"`
}

type nbaPlay struct {
	ID              string `json:"id"`
	SequenceNumber  string `json:"sequenceNumber"`
	Text            string `json:"text"`
	Wallclock       string `json:"wallclock"`
	ShootingPlay    bool   `json:"shootingPlay"`
	ScoringPlay     bool   `json:"scoringPlay"`
	PointsAttempted int    `json:"pointsAttempted"`
	Clock           struct {
		DisplayValue string `json:"displayValue"`
	} `json:"clock"`
	Period struct {
		Number int `json:"number"`
	} `json:"period"`
	Type struct {
		Text string `json:"text"`
	} `json:"type"`
	Coordinate *struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"coordinate"`
	Participants []struct {
		Type    string `json:"type"`
		Athlete struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
			Ref         string `json:"$ref"`
		} `json:"athlete"`
	} `json:"participants"`
	Team *struct {
		ID           string `json:"id"`
		Abbreviation string `json:"abbreviation"`
	} `json:"team"`
}

func FetchNBAScoreboard(ctx context.Context, client *Client) ([]string, error) {
	resp, err := FetchJSON[scoreboardResponse](ctx, client, NBAScoreboardURL)
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

func PollNBAGame(ctx context.Context, client *Client, gameID string, seen map[string]struct{}) (game.GameState, []game.PlayEvent, error) {
	summary, err := FetchJSON[nbaSummaryResponse](ctx, client, fmt.Sprintf(NBASummaryURLFmt, gameID))
	if err != nil {
		return game.GameState{}, nil, err
	}
	plays, err := FetchJSON[nbaPlaysResponse](ctx, client, fmt.Sprintf(NBAPlaysURLFmt, gameID, gameID))
	if err != nil {
		return game.GameState{}, nil, err
	}
	state := normalizeNBAGameState(gameID, summary)
	playerMap, playerTeamMap := nbaPlayerMaps(summary)

	ordered := append([]nbaPlay(nil), plays.Items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return sequenceKey(ordered[i].SequenceNumber, ordered[i].ID) < sequenceKey(ordered[j].SequenceNumber, ordered[j].ID)
	})

	var events []game.PlayEvent
	for _, play := range ordered {
		if !play.ShootingPlay || play.ID == "" {
			continue
		}
		if _, ok := seen[play.ID]; ok {
			continue
		}
		seen[play.ID] = struct{}{}
		events = append(events, normalizeNBAPlay(gameID, play, playerMap, playerTeamMap))
	}
	return state, events, nil
}

func normalizeNBAGameState(gameID string, summary nbaSummaryResponse) game.GameState {
	state := game.GameState{GameID: gameID, Sport: string(game.SportNBA), Status: "live"}
	if len(summary.Header.Competitions) == 0 {
		return state
	}
	comp := summary.Header.Competitions[0]
	state.Status = normalizeStatus(comp.Status.Type.State, comp.Status.Type.Completed)
	state.Period = formatNBAPeriod(comp.Status.Period)
	state.Clock = extractClock(comp.Status.Type.Detail)
	if comp.Situation != nil {
		state.Possession = comp.Situation.Possession
	}
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

func normalizeNBAPlay(gameID string, play nbaPlay, playerMap, playerTeamMap map[string]string) game.PlayEvent {
	playerID, playerName := nbaExtractShooter(play, playerMap)
	location := normalizeNBACoordFromPtr(play.Coordinate)
	data := map[string]any{
		"player_id":   playerID,
		"player_name": playerName,
		"team":        nbaTeamAbbreviation(play, playerTeamMap, playerID),
		"made":        play.ScoringPlay,
		"shot_type":   nbaShotType(play),
		"description": play.Text,
		"period":      formatNBAPeriod(play.Period.Number),
		"clock":       play.Clock.DisplayValue,
	}
	return game.PlayEvent{
		GameID:    gameID,
		PlayID:    play.ID,
		Sport:     string(game.SportNBA),
		Timestamp: game.ParseESPNTime(play.Wallclock),
		Location:  location,
		EventData: data,
	}
}

func normalizeNBACoordFromPtr(coord *struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}) *game.Coord {
	if coord == nil {
		return nil
	}
	if coord.X <= -1000000 || coord.Y <= -1000000 || math.Abs(coord.X) > 1000000 || math.Abs(coord.Y) > 1000000 {
		return nil
	}
	return &game.Coord{X: round2(coord.X * 10), Y: round2(coord.Y * (470.0 / 30.0))}
}

func nbaPlayerMaps(summary nbaSummaryResponse) (map[string]string, map[string]string) {
	players := make(map[string]string)
	teams := make(map[string]string)
	for _, team := range summary.Boxscore.Players {
		abbr := team.Team.Abbreviation
		for _, stat := range team.Statistics {
			for _, athlete := range stat.Athletes {
				id := athlete.Athlete.ID
				if id == "" {
					continue
				}
				players[id] = athlete.Athlete.DisplayName
				teams[id] = abbr
			}
		}
	}
	return players, teams
}

func nbaExtractShooter(play nbaPlay, playerMap map[string]string) (string, string) {
	for _, participant := range play.Participants {
		if participant.Type != "shooter" && participant.Type != "athlete" {
			continue
		}
		id := participant.Athlete.ID
		name := participant.Athlete.DisplayName
		if name == "" {
			name = playerMap[id]
		}
		return id, name
	}
	text := play.Text
	if idx := strings.Index(text, " makes "); idx > 0 {
		return "", strings.TrimSpace(text[:idx])
	}
	if idx := strings.Index(text, " misses "); idx > 0 {
		return "", strings.TrimSpace(text[:idx])
	}
	return "", ""
}

func nbaShotType(play nbaPlay) string {
	text := strings.ToLower(play.Text + " " + play.Type.Text)
	switch {
	case strings.Contains(text, "free throw"):
		return "free_throw"
	case play.PointsAttempted == 3:
		return "3pt jump shot"
	case strings.Contains(text, "3-point") || strings.Contains(text, "three point"):
		return "3pt jump shot"
	default:
		return "2pt shot"
	}
}

func nbaTeamAbbreviation(play nbaPlay, playerTeamMap map[string]string, playerID string) string {
	if play.Team != nil && play.Team.Abbreviation != "" {
		return play.Team.Abbreviation
	}
	return playerTeamMap[playerID]
}

func formatNBAPeriod(period int) string {
	if period <= 4 {
		return fmt.Sprintf("Q%d", period)
	}
	if period == 5 {
		return "OT"
	}
	if period > 5 {
		return fmt.Sprintf("%dOT", period-4)
	}
	return ""
}

func extractClock(detail string) string {
	if idx := strings.LastIndex(detail, " "); idx >= 0 && strings.Contains(detail[:idx], "Q") {
		return strings.TrimSpace(detail[idx+1:])
	}
	parts := strings.Fields(detail)
	for _, part := range parts {
		if strings.Contains(part, ":") {
			return part
		}
	}
	return ""
}

func sequenceKey(seq, fallback string) int {
	if seq != "" {
		if n, err := strconv.Atoi(seq); err == nil {
			return n
		}
	}
	if n, err := strconv.Atoi(fallback); err == nil {
		return n
	}
	return 0
}

func normalizeStatus(state string, completed bool) string {
	if completed || state == "post" {
		return "final"
	}
	if state == "pre" {
		return "pre"
	}
	return "live"
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func marshalRaw(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}
