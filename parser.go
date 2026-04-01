package main

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var athleteRefIDPattern = regexp.MustCompile(`/athletes/(\d+)`)

func buildPlayerMap(summary *summaryResponse) map[string]string {
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

func buildTeamMap(summary *summaryResponse) map[string]string {
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

func inferShotType(play playItem) string {
	text := strings.ToLower(play.Text + " " + play.Type.Text)
	switch {
	case strings.Contains(text, "free throw"):
		return "free_throw"
	case play.PointsAttempted == 3:
		return "3pt"
	case strings.Contains(text, "three point"), strings.Contains(text, "three-pointer"), strings.Contains(text, "3-point"), strings.Contains(text, "3pt"):
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

func extractShooter(play playItem, playerMap map[string]string) (string, string) {
	for _, participant := range play.Participants {
		if participant.Type == "shooter" || participant.Order == 1 {
			id := participant.Athlete.ID
			if id == "" {
				id = athleteIDFromRef(participant.Athlete.Ref)
			}
			name := participant.Athlete.DisplayName
			if name == "" && id != "" {
				name = playerMap[id]
			}
			if name == "" {
				name = extractPlayerNameFromText(play.Text)
			}
			return id, name
		}
	}
	return "", extractPlayerNameFromText(play.Text)
}

func athleteIDFromRef(ref string) string {
	matches := athleteRefIDPattern.FindStringSubmatch(ref)
	if len(matches) == 2 {
		return matches[1]
	}
	return ""
}

func extractPlayerNameFromText(text string) string {
	for _, marker := range []string{" makes ", " misses ", " blocks "} {
		if idx := strings.Index(text, marker); idx > 0 {
			return strings.TrimSpace(text[:idx])
		}
	}
	return ""
}

func teamAbbreviation(play playItem, teamMap map[string]string) string {
	if play.Team != nil && play.Team.Abbreviation != "" {
		return play.Team.Abbreviation
	}
	if play.Team != nil && play.Team.ID != "" {
		if abbr := teamMap[play.Team.ID]; abbr != "" {
			return abbr
		}
	}
	return ""
}

func marshalRawPlay(play playItem) string {
	payload, err := json.Marshal(play)
	if err != nil {
		return "{}"
	}
	return string(payload)
}
