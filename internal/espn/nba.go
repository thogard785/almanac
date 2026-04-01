package espn

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// NBA API URLs
const (
	NBAScoreboardURL = "https://site.api.espn.com/apis/site/v2/sports/basketball/nba/scoreboard"
	NBASummaryURLFmt = "https://site.api.espn.com/apis/site/v2/sports/basketball/nba/summary?event=%s"
	NBAPlaysURLFmt   = "https://sports.core.api.espn.com/v2/sports/basketball/nba/events/%s/competitions/%s/plays?limit=1000"
)

// --- Scoreboard types ---

type ScoreboardResponse struct {
	Events []ScoreboardEvent `json:"events"`
}

type ScoreboardEvent struct {
	ID           string                  `json:"id"`
	Date         string                  `json:"date"`
	Name         string                  `json:"name"`
	ShortName    string                  `json:"shortName"`
	Competitions []ScoreboardCompetition `json:"competitions"`
	Status       ScoreboardStatus        `json:"status"`
}

type ScoreboardCompetition struct {
	ID          string                 `json:"id"`
	Competitors []ScoreboardCompetitor `json:"competitors"`
	Status      ScoreboardStatus       `json:"status"`
}

type ScoreboardCompetitor struct {
	ID       string `json:"id"`
	HomeAway string `json:"homeAway"`
	Score    string `json:"score"`
	Team     struct {
		ID           string `json:"id"`
		Abbreviation string `json:"abbreviation"`
		DisplayName  string `json:"displayName"`
	} `json:"team"`
}

type ScoreboardStatus struct {
	Type ScoreboardStatusType `json:"type"`
}

type ScoreboardStatusType struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	Description string `json:"description"`
	Detail      string `json:"detail"`
	Completed   bool   `json:"completed"`
}

// --- Summary types ---

type SummaryResponse struct {
	Header   SummaryHeader   `json:"header"`
	Boxscore SummaryBoxscore `json:"boxscore"`
}

type SummaryHeader struct {
	Competitions []SummaryCompetition `json:"competitions"`
}

type SummaryCompetition struct {
	Date        string              `json:"date"`
	Status      SummaryStatus       `json:"status"`
	Competitors []SummaryCompetitor `json:"competitors"`
}

type SummaryStatus struct {
	Type SummaryStatusType `json:"type"`
}

type SummaryStatusType struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	Description string `json:"description"`
	Detail      string `json:"detail"`
	Completed   bool   `json:"completed"`
}

type SummaryCompetitor struct {
	ID       string `json:"id"`
	HomeAway string `json:"homeAway"`
	Score    string `json:"score"`
	Team     struct {
		ID           string `json:"id"`
		Abbreviation string `json:"abbreviation"`
		DisplayName  string `json:"displayName"`
	} `json:"team"`
}

type SummaryBoxscore struct {
	Players []SummaryPlayersTeam `json:"players"`
}

type SummaryPlayersTeam struct {
	Team struct {
		ID           string `json:"id"`
		Abbreviation string `json:"abbreviation"`
	} `json:"team"`
	Statistics []SummaryStatCategory `json:"statistics"`
}

type SummaryStatCategory struct {
	Athletes []SummaryStatAthlete `json:"athletes"`
}

type SummaryStatAthlete struct {
	Athlete struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	} `json:"athlete"`
}

// --- Plays types ---

type PlaysResponse struct {
	Items []PlayItem `json:"items"`
	Count int        `json:"count"`
}

type PlayItem struct {
	ID              string `json:"id"`
	SequenceNumber  string `json:"sequenceNumber"`
	Text            string `json:"text"`
	ShortText       string `json:"shortText"`
	Wallclock       string `json:"wallclock"`
	ShootingPlay    bool   `json:"shootingPlay"`
	ScoringPlay     bool   `json:"scoringPlay"`
	PointsAttempted int    `json:"pointsAttempted"`
	Clock           struct {
		Value        float64 `json:"value"`
		DisplayValue string  `json:"displayValue"`
	} `json:"clock"`
	Period struct {
		Number       int    `json:"number"`
		DisplayValue string `json:"displayValue"`
	} `json:"period"`
	Type struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	} `json:"type"`
	Coordinate *struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"coordinate"`
	Participants []PlayParticipant `json:"participants"`
	Team         *struct {
		ID           string `json:"id"`
		Abbreviation string `json:"abbreviation"`
	} `json:"team"`
}

type PlayParticipant struct {
	Type    string `json:"type"`
	Order   int    `json:"order"`
	Athlete struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
		Ref         string `json:"$ref"`
	} `json:"athlete"`
}

// --- ShotEvent preserves V1 format ---

type ShotEvent struct {
	Nonce         int64   `json:"nonce"`
	EventID       string  `json:"event_id"`
	GameID        string  `json:"game_id"`
	TimestampNS   int64   `json:"timestamp_ns"`
	ESPNTimestamp string  `json:"espn_timestamp"`
	Quarter       int     `json:"quarter"`
	GameClock     string  `json:"game_clock"`
	GameClockSecs int     `json:"game_clock_secs"`
	PlayerID      string  `json:"player_id"`
	PlayerName    string  `json:"player_name"`
	Team          string  `json:"team"`
	Made          bool    `json:"made"`
	ShotType      string  `json:"shot_type"`
	LocationX     float64 `json:"location_x"`
	LocationY     float64 `json:"location_y"`
	LocationZone  string  `json:"location_zone"`
	Description   string  `json:"description"`
	RawPayload    string  `json:"raw_payload"`
}

// ShotTypeLabel returns human-readable shot type.
func (s ShotEvent) ShotTypeLabel() string {
	switch s.ShotType {
	case "3pt":
		return "3PT"
	case "2pt":
		return "2PT"
	case "free_throw":
		return "FT"
	default:
		return s.ShotType
	}
}

// --- NBA Parser ---

type NBAParser struct {
	client *Client
}

func NewNBAParser(client *Client) *NBAParser {
	return &NBAParser{client: client}
}

// FetchScoreboard returns all games on today's NBA scoreboard.
func (p *NBAParser) FetchScoreboard(ctx context.Context) ([]ScoreboardEvent, error) {
	resp, err := FetchJSON[ScoreboardResponse](ctx, p.client, NBAScoreboardURL)
	if err != nil {
		return nil, err
	}
	return resp.Events, nil
}

// GameInfo holds metadata from the summary endpoint.
type GameInfo struct {
	GameID    string
	StartTime string
	Status    string
	State     string
	Detail    string
	Completed bool
	HomeTeam  string
	AwayTeam  string
	HomeScore string
	AwayScore string
}

// FetchGameInfo returns metadata for a game.
func (p *NBAParser) FetchGameInfo(ctx context.Context, gameID string) (*GameInfo, error) {
	summary, err := FetchJSON[SummaryResponse](ctx, p.client, fmt.Sprintf(NBASummaryURLFmt, gameID))
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

// FetchShots returns new shot events for a game. The seen map tracks already-processed play IDs.
func (p *NBAParser) FetchShots(ctx context.Context, gameID string, seen map[string]struct{}) ([]ShotEvent, *GameInfo, error) {
	summary, err := FetchJSON[SummaryResponse](ctx, p.client, fmt.Sprintf(NBASummaryURLFmt, gameID))
	if err != nil {
		return nil, nil, fmt.Errorf("fetch summary: %w", err)
	}
	plays, err := FetchJSON[PlaysResponse](ctx, p.client, fmt.Sprintf(NBAPlaysURLFmt, gameID, gameID))
	if err != nil {
		return nil, nil, fmt.Errorf("fetch plays: %w", err)
	}

	playerMap := buildPlayerMap(&summary)
	playerNameToID := buildPlayerNameToIDMap(&summary)
	teamMap := buildTeamMap(&summary)
	playerTeamMap := buildPlayerTeamMap(&summary)

	// Sort by sequence
	ordered := make([]PlayItem, len(plays.Items))
	copy(ordered, plays.Items)
	sort.SliceStable(ordered, func(i, j int) bool {
		return playSortKey(ordered[i]) < playSortKey(ordered[j])
	})

	var info *GameInfo
	if len(summary.Header.Competitions) > 0 {
		comp := summary.Header.Competitions[0]
		info = &GameInfo{
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
	}

	var shots []ShotEvent
	for _, play := range ordered {
		if !play.ShootingPlay || play.ID == "" {
			continue
		}
		if _, exists := seen[play.ID]; exists {
			continue
		}
		shot := convertNBAPlay(gameID, play, playerMap, playerNameToID, teamMap, playerTeamMap)
		shots = append(shots, shot)
	}
	return shots, info, nil
}

func convertNBAPlay(gameID string, play PlayItem, playerMap, playerNameToID, teamMap, playerTeamMap map[string]string) ShotEvent {
	playerID, playerName := extractShooter(play, playerMap, playerNameToID)
	shotType := inferShotType(play)
	team := teamAbbreviation(play, teamMap, playerTeamMap, playerID)
	locationX, locationY := 0.0, 0.0
	if play.Coordinate != nil {
		locationX = play.Coordinate.X
		locationY = play.Coordinate.Y
	}
	zone := inferLocationZone(locationX, locationY, shotType)
	if play.Coordinate == nil {
		zone = "missing"
	} else if isInvalidCoordinate(play.Coordinate.X, play.Coordinate.Y) {
		if shotType == "free_throw" {
			// ESPN uses sentinel coordinates for free throws; normalize them back to the
			// documented 0,0 placeholder so downstream renderers do not fling markers off-court.
			locationX = 0
			locationY = 0
			zone = "free_throw"
		} else {
			zone = "invalid"
		}
	}

	return ShotEvent{
		EventID:       play.ID,
		GameID:        gameID,
		ESPNTimestamp: play.Wallclock,
		Quarter:       play.Period.Number,
		GameClock:     play.Clock.DisplayValue,
		GameClockSecs: parseGameClockSeconds(play.Clock.DisplayValue),
		PlayerID:      playerID,
		PlayerName:    playerName,
		Team:          team,
		Made:          play.ScoringPlay,
		ShotType:      shotType,
		LocationX:     locationX,
		LocationY:     locationY,
		LocationZone:  zone,
		Description:   play.Text,
		RawPayload:    marshalRawPlay(play),
	}
}

// --- Helper functions (ported from V1 parser.go) ---

var athleteRefIDPattern = regexp.MustCompile(`/athletes/(\d+)`)

func buildPlayerMap(summary *SummaryResponse) map[string]string {
	players := make(map[string]string)
	for _, team := range summary.Boxscore.Players {
		for _, category := range team.Statistics {
			for _, athlete := range category.Athletes {
				if athlete.Athlete.ID != "" && athlete.Athlete.DisplayName != "" {
					players[athlete.Athlete.ID] = athlete.Athlete.DisplayName
				}
			}
		}
	}
	return players
}

func buildPlayerNameToIDMap(summary *SummaryResponse) map[string]string {
	nameToID := make(map[string]string)
	for _, team := range summary.Boxscore.Players {
		for _, category := range team.Statistics {
			for _, athlete := range category.Athletes {
				if athlete.Athlete.ID != "" && athlete.Athlete.DisplayName != "" {
					nameToID[normalizeName(athlete.Athlete.DisplayName)] = athlete.Athlete.ID
				}
			}
		}
	}
	return nameToID
}

func buildTeamMap(summary *SummaryResponse) map[string]string {
	teams := make(map[string]string)
	if len(summary.Header.Competitions) == 0 {
		return teams
	}
	for _, competitor := range summary.Header.Competitions[0].Competitors {
		id := competitor.Team.ID
		if id == "" {
			id = competitor.ID
		}
		abbr := competitor.Team.Abbreviation
		if id != "" && abbr != "" {
			teams[id] = abbr
		}
	}
	return teams
}

func buildPlayerTeamMap(summary *SummaryResponse) map[string]string {
	playerTeams := make(map[string]string)
	for _, team := range summary.Boxscore.Players {
		abbr := team.Team.Abbreviation
		for _, category := range team.Statistics {
			for _, athlete := range category.Athletes {
				if athlete.Athlete.ID != "" && abbr != "" {
					playerTeams[athlete.Athlete.ID] = abbr
				}
			}
		}
	}
	return playerTeams
}

func parseGameClockSeconds(display string) int {
	display = strings.TrimSpace(display)
	if display == "" {
		return 0
	}
	if strings.Contains(display, ":") {
		parts := strings.SplitN(display, ":", 2)
		mins, _ := strconv.Atoi(parts[0])
		secs, _ := strconv.Atoi(parts[1])
		return mins*60 + secs
	}
	value, _ := strconv.ParseFloat(display, 64)
	return int(value)
}

func inferShotType(play PlayItem) string {
	text := strings.ToLower(play.Text + " " + play.Type.Text)
	switch {
	case strings.Contains(text, "free throw"):
		return "free_throw"
	case play.PointsAttempted == 3:
		return "3pt"
	case strings.Contains(text, "three point"), strings.Contains(text, "three-pointer"),
		strings.Contains(text, "3-point"), strings.Contains(text, "3pt"):
		return "3pt"
	default:
		return "2pt"
	}
}

func inferLocationZone(x, y float64, shotType string) string {
	if shotType == "free_throw" {
		return "free_throw"
	}
	if isInvalidCoordinate(x, y) {
		return "invalid"
	}
	dx := x - 25.0
	dy := y - 1.5
	dist := math.Sqrt(dx*dx + dy*dy)
	if y <= 4 && math.Abs(dx) <= 4 {
		return "restricted_area"
	}
	if y <= 10 && math.Abs(dx) <= 8 {
		return "paint"
	}
	if shotType == "3pt" {
		side := "center"
		if dx <= -8 {
			side = "left"
		} else if dx >= 8 {
			side = "right"
		}
		if y <= 6 {
			return side + "_corner_3"
		}
		return side + "_arc_3"
	}
	if dist >= 18 {
		return "deep_mid_range"
	}
	if dx <= -8 {
		return "left_mid_range"
	}
	if dx >= 8 {
		return "right_mid_range"
	}
	return "center_mid_range"
}

func isInvalidCoordinate(x, y float64) bool {
	return x <= -1000000 || y <= -1000000 || math.Abs(x) > 1000000 || math.Abs(y) > 1000000
}

func extractShooter(play PlayItem, playerMap, playerNameToID map[string]string) (string, string) {
	if name := extractPlayerNameFromText(play.Text); name != "" {
		if id := playerNameToID[normalizeName(name)]; id != "" {
			return id, name
		}
		return "", name
	}
	for _, participant := range play.Participants {
		if participant.Type == "shooter" {
			id := participant.Athlete.ID
			if id == "" {
				id = athleteIDFromRef(participant.Athlete.Ref)
			}
			name := participant.Athlete.DisplayName
			if name == "" && id != "" {
				name = playerMap[id]
			}
			return id, name
		}
	}
	for _, participant := range play.Participants {
		id := participant.Athlete.ID
		if id == "" {
			id = athleteIDFromRef(participant.Athlete.Ref)
		}
		name := participant.Athlete.DisplayName
		if name == "" && id != "" {
			name = playerMap[id]
		}
		if id != "" || name != "" {
			return id, name
		}
	}
	return "", ""
}

func athleteIDFromRef(ref string) string {
	matches := athleteRefIDPattern.FindStringSubmatch(ref)
	if len(matches) == 2 {
		return matches[1]
	}
	return ""
}

func extractPlayerNameFromText(text string) string {
	if idx := strings.Index(text, " makes "); idx > 0 {
		return strings.TrimSpace(text[:idx])
	}
	if idx := strings.Index(text, " misses "); idx > 0 {
		return strings.TrimSpace(text[:idx])
	}
	if idx := strings.Index(text, " blocks "); idx > 0 {
		rest := text[idx+len(" blocks "):]
		if end := strings.Index(rest, "'s "); end > 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	return ""
}

func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func teamAbbreviation(play PlayItem, teamMap, playerTeamMap map[string]string, playerID string) string {
	if play.Team != nil && play.Team.Abbreviation != "" {
		return play.Team.Abbreviation
	}
	if play.Team != nil && play.Team.ID != "" {
		if abbr := teamMap[play.Team.ID]; abbr != "" {
			return abbr
		}
	}
	if playerID != "" {
		if abbr := playerTeamMap[playerID]; abbr != "" {
			return abbr
		}
	}
	return ""
}

func marshalRawPlay(play PlayItem) string {
	payload, err := json.Marshal(play)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func playSortKey(play PlayItem) int {
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
