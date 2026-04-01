package espn

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
)

// Golf API URLs
const (
	GolfScoreboardURL = "https://site.api.espn.com/apis/site/v2/sports/golf/pga/scoreboard"
	GolfSummaryURLFmt = "https://site.api.espn.com/apis/site/v2/sports/golf/pga/summary?event=%s"
	GolfPlaysURLFmt   = "https://sports.core.api.espn.com/v2/sports/golf/leagues/pga/events/%s/competitions/%s/plays?limit=1000"
)

// GolfPlaysResponse is the response from the golf plays endpoint.
type GolfPlaysResponse struct {
	Items []GolfPlayItem `json:"items"`
	Count int            `json:"count"`
}

// GolfPlayItem represents a single play from the golf API.
// Note: ESPN golf play-by-play may not have coordinate data.
// The API returns empty items for tournaments not yet started.
// For active tournaments, plays contain hole/shot info but location
// data availability is uncertain — we handle both cases.
type GolfPlayItem struct {
	ID             string `json:"id"`
	SequenceNumber string `json:"sequenceNumber"`
	Text           string `json:"text"`
	Wallclock      string `json:"wallclock"`
	ScoringPlay    bool   `json:"scoringPlay"`
	Period         struct {
		Number       int    `json:"number"` // hole number
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
	Participants []struct {
		Type    string `json:"type"`
		Athlete struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
			Ref         string `json:"$ref"`
		} `json:"athlete"`
	} `json:"participants"`
	// Golf-specific fields that may be present
	Shot *struct {
		Number int `json:"number"`
	} `json:"shot"`
}

// GolfShotEvent represents a golf shot/play event.
type GolfShotEvent struct {
	EventID       string  `json:"event_id"`
	GameID        string  `json:"game_id"` // tournament ID
	TimestampNS   int64   `json:"timestamp_ns"`
	ESPNTimestamp string  `json:"espn_timestamp"`
	Hole          int     `json:"hole"`
	ShotNumber    int     `json:"shot_number"`
	PlayerID      string  `json:"player_id"`
	PlayerName    string  `json:"player_name"`
	LocationX     float64 `json:"location_x"`
	LocationY     float64 `json:"location_y"`
	HasCoords     bool    `json:"has_coords"`
	Zone          string  `json:"zone"` // hole-based zone if no coords
	Description   string  `json:"description"`
	RawPayload    string  `json:"raw_payload"`
}

// GolfParser handles PGA golf ESPN data.
type GolfParser struct {
	client *Client
}

func NewGolfParser(client *Client) *GolfParser {
	return &GolfParser{client: client}
}

// FetchScoreboard returns current golf events/tournaments.
func (p *GolfParser) FetchScoreboard(ctx context.Context) ([]ScoreboardEvent, error) {
	resp, err := FetchJSON[ScoreboardResponse](ctx, p.client, GolfScoreboardURL)
	if err != nil {
		return nil, err
	}
	return resp.Events, nil
}

// FetchGameInfo returns tournament info.
func (p *GolfParser) FetchGameInfo(ctx context.Context, eventID string) (*GameInfo, error) {
	// Golf uses the scoreboard since summary may 502
	events, err := p.FetchScoreboard(ctx)
	if err != nil {
		return nil, err
	}
	for _, ev := range events {
		if ev.ID == eventID {
			info := &GameInfo{
				GameID: eventID,
				Status: ev.Status.Type.Name,
				State:  ev.Status.Type.State,
				Detail: ev.Status.Type.Detail,
				Completed: ev.Status.Type.Completed,
			}
			if len(ev.Competitions) > 0 {
				info.StartTime = ev.Date
			}
			return info, nil
		}
	}
	return nil, fmt.Errorf("golf event %s not found", eventID)
}

// FetchShots returns new golf shot events for a tournament.
func (p *GolfParser) FetchShots(ctx context.Context, eventID string, seen map[string]struct{}) ([]GolfShotEvent, *GameInfo, error) {
	info, err := p.FetchGameInfo(ctx, eventID)
	if err != nil {
		return nil, nil, err
	}

	plays, err := FetchJSON[GolfPlaysResponse](ctx, p.client, fmt.Sprintf(GolfPlaysURLFmt, eventID, eventID))
	if err != nil {
		// Golf plays endpoint may return errors for some tournaments
		return nil, info, nil
	}

	var shots []GolfShotEvent
	for _, play := range plays.Items {
		if play.ID == "" {
			continue
		}
		if _, exists := seen[play.ID]; exists {
			continue
		}
		shot := convertGolfPlay(eventID, play)
		shots = append(shots, shot)
	}
	return shots, info, nil
}

func convertGolfPlay(eventID string, play GolfPlayItem) GolfShotEvent {
	ev := GolfShotEvent{
		EventID:       play.ID,
		GameID:        eventID,
		ESPNTimestamp: play.Wallclock,
		Hole:          play.Period.Number,
		Description:   play.Text,
		RawPayload:    marshalGolfPlay(play),
	}

	if play.Shot != nil {
		ev.ShotNumber = play.Shot.Number
	}

	if play.Coordinate != nil {
		ev.LocationX = play.Coordinate.X
		ev.LocationY = play.Coordinate.Y
		ev.HasCoords = true
	}

	// Zone defaults to hole number if no coords
	if !ev.HasCoords {
		ev.Zone = fmt.Sprintf("hole_%d", ev.Hole)
	}

	for _, p := range play.Participants {
		id := p.Athlete.ID
		if id == "" {
			id = athleteIDFromRef(p.Athlete.Ref)
		}
		ev.PlayerID = id
		ev.PlayerName = p.Athlete.DisplayName
		break
	}

	return ev
}

// GolfWinCheck checks if a golf bet wins.
// If coordinates available: within radius. Otherwise: zone match.
func GolfWinCheck(predictedX, predictedY float64, predictedZone string, actual GolfShotEvent, radius float64) (won bool, dist float64) {
	if actual.HasCoords && predictedX != 0 && predictedY != 0 {
		dx := predictedX - actual.LocationX
		dy := predictedY - actual.LocationY
		dist = math.Sqrt(dx*dx + dy*dy)
		return dist <= radius, dist
	}
	if predictedZone != "" && actual.Zone != "" {
		return predictedZone == actual.Zone, 0
	}
	return false, 0
}

func marshalGolfPlay(play GolfPlayItem) string {
	payload, err := json.Marshal(play)
	if err != nil {
		return "{}"
	}
	return string(payload)
}
