package main

import "time"

// ShotEvent is the persisted shot payload consumed by downstream clients.
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

type LocationSchema struct {
	Description string     `json:"source"`
	XRange      [2]float64 `json:"x_range"`
	YRange      [2]float64 `json:"y_range"`
	Origin      string     `json:"origin"`
	Units       string     `json:"units"`
	Notes       string     `json:"notes"`
}

type ShotStore struct {
	GameID         string         `json:"game_id"`
	GameStartTime  string         `json:"game_start_time_utc"`
	LastUpdated    string         `json:"last_updated"`
	LocationSchema LocationSchema `json:"coordinate_system"`
	Shots          []ShotEvent    `json:"shots"`
}

type Config struct {
	GameID       string
	PollInterval time.Duration
	OutputPath   string
}

type summaryResponse struct {
	Header   summaryHeader   `json:"header"`
	Boxscore summaryBoxscore `json:"boxscore"`
}

type summaryHeader struct {
	Competitions []summaryCompetition `json:"competitions"`
}

type summaryCompetition struct {
	Date        string              `json:"date"`
	Status      summaryStatus       `json:"status"`
	Competitors []summaryCompetitor `json:"competitors"`
}

type summaryStatus struct {
	Type summaryStatusType `json:"type"`
}

type summaryStatusType struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	Description string `json:"description"`
	Detail      string `json:"detail"`
	Completed   bool   `json:"completed"`
}

type summaryCompetitor struct {
	ID       string `json:"id"`
	HomeAway string `json:"homeAway"`
	Score    string `json:"score"`
	Team     struct {
		ID           string `json:"id"`
		Abbreviation string `json:"abbreviation"`
		DisplayName  string `json:"displayName"`
	} `json:"team"`
}

type summaryBoxscore struct {
	Players []summaryPlayersTeam `json:"players"`
}

type summaryPlayersTeam struct {
	Team struct {
		ID           string `json:"id"`
		Abbreviation string `json:"abbreviation"`
	} `json:"team"`
	Statistics []summaryStatCategory `json:"statistics"`
}

type summaryStatCategory struct {
	Athletes []summaryStatAthlete `json:"athletes"`
}

type summaryStatAthlete struct {
	Athlete struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	} `json:"athlete"`
}

type playsResponse struct {
	Items []playItem `json:"items"`
	Count int        `json:"count"`
}

type playItem struct {
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
	Participants []struct {
		Type    string `json:"type"`
		Order   int    `json:"order"`
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
