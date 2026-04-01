package game

import (
	"strconv"
	"strings"
	"time"
)

// Sport identifies a supported sport.
type Sport string

const (
	SportNBA  Sport = "nba"
	SportMLB  Sport = "mlb"
	SportGolf Sport = "golf"
)

// Coord is a normalized point in the frontend SVG coordinate space.
type Coord struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// PlayEvent is the unified event pushed to frontend clients.
type PlayEvent struct {
	GameID    string    `json:"game_id"`
	PlayID    string    `json:"play_id"`
	Sport     string    `json:"sport"`
	Timestamp time.Time `json:"timestamp"`
	Location  *Coord    `json:"location"`
	EventData any       `json:"event"`
}

// GameState is the normalized live game snapshot sent to all clients.
type GameState struct {
	GameID     string `json:"game_id"`
	Sport      string `json:"sport"`
	Status     string `json:"status"`
	State      string `json:"state,omitempty"`
	Detail     string `json:"detail,omitempty"`
	StartTime  string `json:"start_time,omitempty"`
	Home       string `json:"home"`
	Away       string `json:"away"`
	HomeScore  int    `json:"home_score"`
	AwayScore  int    `json:"away_score"`
	Period     string `json:"period"`
	Clock      string `json:"clock"`
	Possession string `json:"possession,omitempty"`
	Completed  bool   `json:"completed"`
	Tracked    bool   `json:"tracked"`
}

func ParseESPNTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func ParseScoreInt(value string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(value))
	return n
}
