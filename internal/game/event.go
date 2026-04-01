package game

import (
	"time"

	"github.com/almanac/espn-shots/internal/espn"
)

// Sport identifies a supported sport.
type Sport string

const (
	SportNBA Sport = "nba"
	SportMLB Sport = "mlb"
	SportPGA Sport = "pga"
)

// SportEvent is the unified interface for all sport play events.
type SportEvent interface {
	GetEventID() string
	GetGameID() string
	GetSport() Sport
	GetTimestamp() string // ESPN wallclock
	GetLocationX() float64
	GetLocationY() float64
	GetZone() string
	HasCoordinates() bool
}

// NBASportEvent wraps an NBA ShotEvent as a SportEvent.
type NBASportEvent struct {
	espn.ShotEvent
}

func (e *NBASportEvent) GetEventID() string    { return e.EventID }
func (e *NBASportEvent) GetGameID() string     { return e.GameID }
func (e *NBASportEvent) GetSport() Sport       { return SportNBA }
func (e *NBASportEvent) GetTimestamp() string  { return e.ESPNTimestamp }
func (e *NBASportEvent) GetLocationX() float64 { return e.LocationX }
func (e *NBASportEvent) GetLocationY() float64 { return e.LocationY }
func (e *NBASportEvent) GetZone() string       { return e.LocationZone }
func (e *NBASportEvent) HasCoordinates() bool {
	return e.LocationZone != "missing" && e.LocationZone != "invalid"
}

// MLBSportEvent wraps an MLB PitchEvent as a SportEvent.
type MLBSportEvent struct {
	espn.PitchEvent
}

func (e *MLBSportEvent) GetEventID() string    { return e.EventID }
func (e *MLBSportEvent) GetGameID() string     { return e.GameID }
func (e *MLBSportEvent) GetSport() Sport       { return SportMLB }
func (e *MLBSportEvent) GetTimestamp() string  { return e.ESPNTimestamp }
func (e *MLBSportEvent) GetLocationX() float64 { return e.PitchX }
func (e *MLBSportEvent) GetLocationY() float64 { return e.PitchY }
func (e *MLBSportEvent) GetZone() string       { return e.Zone }
func (e *MLBSportEvent) HasCoordinates() bool  { return e.HasPitchCoords }

// GolfSportEvent wraps a Golf shot event as a SportEvent.
type GolfSportEvent struct {
	espn.GolfShotEvent
}

func (e *GolfSportEvent) GetEventID() string    { return e.EventID }
func (e *GolfSportEvent) GetGameID() string     { return e.GolfShotEvent.GameID }
func (e *GolfSportEvent) GetSport() Sport       { return SportPGA }
func (e *GolfSportEvent) GetTimestamp() string  { return e.ESPNTimestamp }
func (e *GolfSportEvent) GetLocationX() float64 { return e.LocationX }
func (e *GolfSportEvent) GetLocationY() float64 { return e.LocationY }
func (e *GolfSportEvent) GetZone() string       { return e.GolfShotEvent.Zone }
func (e *GolfSportEvent) HasCoordinates() bool  { return e.HasCoords }

// TimestampToUnixNano parses an ESPN wallclock timestamp to Unix nanoseconds.
func TimestampToUnixNano(ts string) int64 {
	if ts == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05Z", ts)
		if err != nil {
			return 0
		}
	}
	return t.UnixNano()
}
