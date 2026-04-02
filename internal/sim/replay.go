package sim

import (
	"context"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/almanac/espn-shots/internal/game"
)

const simGamePrefix = "sim:"

// SimGameID returns the simulation-namespaced game ID.
func SimGameID(originalID string) string { return simGamePrefix + originalID }

// Replayer replays a saved completed game with time-shifted timestamps,
// making it appear live to simulation-mode clients.
type Replayer struct {
	mu        sync.RWMutex
	saved     *SavedGame
	simID     string
	offset    time.Duration // add to original timestamp to get sim time
	startAt   time.Time
	nextIdx   int
	state     game.GameState
	done      bool
	onEvent   func(game.PlayEvent)
	home      string
	away      string
	homeScore int
	awayScore int
}

// NewReplayer creates a replayer for the given saved game. Events are emitted
// through onEvent. The replay starts when Run is called.
func NewReplayer(saved *SavedGame, onEvent func(game.PlayEvent)) *Replayer {
	// Sort plays by timestamp.
	plays := make([]game.PlayEvent, len(saved.Plays))
	copy(plays, saved.Plays)
	sort.SliceStable(plays, func(i, j int) bool {
		return plays[i].Timestamp.Before(plays[j].Timestamp)
	})
	savedCopy := *saved
	savedCopy.Plays = plays

	r := &Replayer{
		saved:   &savedCopy,
		simID:   SimGameID(saved.GameID),
		onEvent: onEvent,
		home:    saved.State.Home,
		away:    saved.State.Away,
		state: game.GameState{
			GameID:     SimGameID(saved.GameID),
			Sport:      saved.Sport,
			Status:     "in_progress",
			State:      "in",
			Home:       saved.State.Home,
			Away:       saved.State.Away,
			Tracked:    true,
			Simulation: true,
		},
	}
	return r
}

// GameID returns the simulation game ID.
func (r *Replayer) GameID() string { return r.simID }

// Done returns true when all plays have been emitted.
func (r *Replayer) Done() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.done
}

// GameState returns the current simulation game state snapshot.
func (r *Replayer) GameState() game.GameState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

// Run starts the replay loop. Blocks until ctx is cancelled or all plays are emitted.
func (r *Replayer) Run(ctx context.Context) {
	now := time.Now()
	r.mu.Lock()
	if len(r.saved.Plays) == 0 {
		r.done = true
		r.mu.Unlock()
		return
	}
	r.startAt = now
	r.offset = now.Sub(r.saved.Plays[0].Timestamp)
	r.state.StartTime = now.UTC().Format(time.RFC3339)
	r.mu.Unlock()

	log.Printf("[sim-replay] starting replay of %s (%d plays)", r.saved.GameID, len(r.saved.Plays))

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if r.emitDue() {
				return
			}
		}
	}
}

// emitDue emits all plays that should have fired by now. Returns true when replay is complete.
func (r *Replayer) emitDue() bool {
	now := time.Now()
	r.mu.Lock()
	var toEmit []game.PlayEvent
	for r.nextIdx < len(r.saved.Plays) {
		play := r.saved.Plays[r.nextIdx]
		shiftedTime := play.Timestamp.Add(r.offset)
		if shiftedTime.After(now) {
			break
		}
		// Rewrite play for sim context.
		play.GameID = r.simID
		play.PlayID = "sim_" + play.PlayID
		play.Timestamp = shiftedTime
		toEmit = append(toEmit, play)
		r.updateStateLocked(play)
		r.nextIdx++
	}
	allDone := r.nextIdx >= len(r.saved.Plays)
	if allDone {
		r.state.Status = "final"
		r.state.State = "post"
		r.state.Completed = true
		r.done = true
	}
	r.mu.Unlock()

	for _, ev := range toEmit {
		if r.onEvent != nil {
			r.onEvent(ev)
		}
	}
	return allDone
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
	// Track score from scoring plays.
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

// parseIntDefault parses a string as int, returning def on failure.
func parseIntDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
