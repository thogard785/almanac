package sim

import (
	"sort"
	"strings"
	"time"

	"github.com/almanac/espn-shots/internal/game"
)

const simGamePrefix = "sim:"

// SimGameID returns the simulation-namespaced game ID.
func SimGameID(originalID string) string { return simGamePrefix + originalID }

// SimPlayID returns the simulation-namespaced play ID for a live-source play.
func SimPlayID(sourceGameID, sourcePlayID string) string {
	return simGamePrefix + sourceGameID + ":" + sourcePlayID
}

type ReplayEmission struct {
	Event       game.PlayEvent
	State       game.GameState
	SourceEvent game.PlayEvent
	Sequence    int
	EmitAt      time.Time
}

// Replayer replays a saved completed game with synthetic timing suitable for
// backend-driven simulation when no live games are available.
type Replayer struct {
	saved         *SavedGame
	simID         string
	eventSpacing  time.Duration
	started       bool
	startAt       time.Time
	nextReleaseAt time.Time
	nextIdx       int
	state         game.GameState
	done          bool
	home          string
	away          string
	homeScore     int
	awayScore     int
}

// NewReplayer creates a replayer for the given saved game. Plays are replayed
// in source order with a fixed synthetic cadence so off-hours testing advances
// on a useful timescale instead of taking real-game wall-clock duration.
func NewReplayer(saved *SavedGame, eventSpacing time.Duration) *Replayer {
	if eventSpacing <= 0 {
		eventSpacing = time.Second
	}

	plays := make([]game.PlayEvent, len(saved.Plays))
	copy(plays, saved.Plays)
	sort.SliceStable(plays, func(i, j int) bool {
		return plays[i].Timestamp.Before(plays[j].Timestamp)
	})
	savedCopy := *saved
	savedCopy.Plays = plays

	status := strings.TrimSpace(saved.State.Status)
	if status == "" {
		status = "in_progress"
	}
	stateValue := strings.TrimSpace(saved.State.State)
	if stateValue == "" || strings.EqualFold(stateValue, "post") {
		stateValue = "in"
	}
	detail := strings.TrimSpace(saved.State.Detail)
	if detail == "" {
		detail = "Archived replay"
	}

	return &Replayer{
		saved:        &savedCopy,
		simID:        SimGameID(saved.GameID),
		eventSpacing: eventSpacing,
		home:         saved.State.Home,
		away:         saved.State.Away,
		state: game.GameState{
			GameID:     SimGameID(saved.GameID),
			Sport:      saved.Sport,
			Status:     status,
			State:      stateValue,
			Detail:     detail,
			Home:       saved.State.Home,
			Away:       saved.State.Away,
			Tracked:    true,
			Simulation: true,
		},
	}
}

// Start anchors the replay to the current wall clock.
func (r *Replayer) Start(now time.Time) {
	if r.started || len(r.saved.Plays) == 0 {
		if len(r.saved.Plays) == 0 {
			r.done = true
		}
		return
	}
	r.started = true
	r.startAt = now.UTC()
	r.nextReleaseAt = r.startAt.Add(r.eventSpacing)
	r.state.StartTime = r.startAt.Format(time.RFC3339)
}

// GameID returns the simulation game ID.
func (r *Replayer) GameID() string { return r.simID }

// StartAt returns when the synthetic replay began.
func (r *Replayer) StartAt() time.Time { return r.startAt }

// Done returns true when all plays have been emitted.
func (r *Replayer) Done() bool { return r.done }

// GameState returns the current simulation game snapshot.
func (r *Replayer) GameState() game.GameState { return r.state }

// EmitDue emits any replay steps whose synthetic release time has arrived.
func (r *Replayer) EmitDue(now time.Time) []ReplayEmission {
	if !r.started || r.done {
		return nil
	}

	var emissions []ReplayEmission
	for r.nextIdx < len(r.saved.Plays) && !r.nextReleaseAt.After(now) {
		source := r.saved.Plays[r.nextIdx]
		emitAt := r.nextReleaseAt.UTC()
		simEvent := game.PlayEvent{
			GameID:    r.simID,
			PlayID:    SimPlayID(r.saved.GameID, source.PlayID),
			Sport:     source.Sport,
			Timestamp: emitAt,
			Location:  source.Location,
			EventData: source.EventData,
		}
		r.updateStateLocked(source)
		r.nextIdx++
		if r.nextIdx >= len(r.saved.Plays) {
			r.state.Status = finalStatus(r.saved.State.Status)
			r.state.State = finalState(r.saved.State.State)
			r.state.Detail = strings.TrimSpace(r.saved.State.Detail)
			r.state.Completed = true
			r.done = true
		}
		emissions = append(emissions, ReplayEmission{
			Event:       simEvent,
			State:       r.state,
			SourceEvent: source,
			Sequence:    r.nextIdx,
			EmitAt:      emitAt,
		})
		r.nextReleaseAt = r.nextReleaseAt.Add(r.eventSpacing)
	}
	return emissions
}

func (r *Replayer) updateStateLocked(play game.PlayEvent) {
	data, ok := play.EventData.(map[string]any)
	if !ok {
		return
	}
	if p, ok := data["period"]; ok {
		if s, ok := p.(string); ok {
			r.state.Period = s
		}
	}
	if c, ok := data["clock"]; ok {
		if s, ok := c.(string); ok {
			r.state.Clock = s
		}
	}
	if team, ok := data["possession"].(string); ok {
		r.state.Possession = team
	}
	made, _ := data["made"].(bool)
	if !made {
		return
	}
	team, _ := data["team"].(string)
	points := pointsFromShotType(data)
	if strings.EqualFold(team, r.home) {
		r.homeScore += points
		r.state.HomeScore = r.homeScore
	} else if strings.EqualFold(team, r.away) {
		r.awayScore += points
		r.state.AwayScore = r.awayScore
	}
}

func finalStatus(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "final"
	}
	return value
}

func finalState(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "in") {
		return "post"
	}
	return value
}

func pointsFromShotType(data map[string]any) int {
	st, _ := data["shot_type"].(string)
	st = strings.ToLower(st)
	switch {
	case strings.Contains(st, "free"):
		return 1
	case strings.Contains(st, "3pt") || strings.Contains(st, "three"):
		return 3
	default:
		return 2
	}
}
