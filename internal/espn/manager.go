package espn

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/almanac/espn-shots/internal/game"
)

type EventHandler func(game.PlayEvent)

// CompletionHandler is called when a tracked game transitions to completed.
// It receives the final game state and all play events observed during tracking.
type CompletionHandler func(gameID string, sport game.Sport, state game.GameState, plays []game.PlayEvent)

type Manager struct {
	client            *Client
	pollInterval      time.Duration
	eventHandler      EventHandler
	completionHandler CompletionHandler

	mu      sync.RWMutex
	tracked map[string]*trackedGame
}

type trackedGame struct {
	sport    game.Sport
	id       string
	seen     map[string]struct{}
	state    game.GameState
	allPlays []game.PlayEvent // all play events emitted for this game
}

func NewManager(client *Client, pollInterval time.Duration, eventHandler EventHandler) *Manager {
	if pollInterval <= 0 {
		pollInterval = 3 * time.Second
	}
	return &Manager{
		client:       client,
		pollInterval: pollInterval,
		eventHandler: eventHandler,
		tracked:      make(map[string]*trackedGame),
	}
}

// SetCompletionHandler registers a callback invoked when a game finishes.
func (m *Manager) SetCompletionHandler(h CompletionHandler) {
	m.completionHandler = h
}

func (m *Manager) Run(ctx context.Context) {
	go m.discoveryLoop(ctx)
	go m.pollLoop(ctx)
}

func (m *Manager) Games() []game.GameState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]game.GameState, 0, len(m.tracked))
	for _, tg := range m.tracked {
		if tg.state.GameID == "" {
			continue
		}
		out = append(out, tg.state)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sport == out[j].Sport {
			return out[i].GameID < out[j].GameID
		}
		return out[i].Sport < out[j].Sport
	})
	return out
}

func (m *Manager) Game(gameID string) (game.GameState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, tg := range m.tracked {
		if tg.state.GameID == gameID {
			return tg.state, true
		}
	}
	return game.GameState{}, false
}

func (m *Manager) discoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	m.discover(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.discover(ctx)
		}
	}
}

func (m *Manager) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	m.pollAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.pollAll(ctx)
		}
	}
}

func (m *Manager) discover(ctx context.Context) {
	for _, sport := range []game.Sport{game.SportNBA, game.SportMLB, game.SportGolf} {
		func() {
			defer recoverLog(fmt.Sprintf("discover %s", sport))
			ids, err := m.fetchScoreboard(ctx, sport)
			if err != nil {
				log.Printf("[espn] %s scoreboard error: %v", sport, err)
				return
			}
			m.mu.Lock()
			for _, id := range ids {
				key := trackedKey(sport, id)
				if _, exists := m.tracked[key]; exists {
					continue
				}
				m.tracked[key] = &trackedGame{sport: sport, id: id, seen: make(map[string]struct{})}
			}
			m.mu.Unlock()
		}()
	}
}

func (m *Manager) pollAll(ctx context.Context) {
	m.mu.RLock()
	liveGames := make([]*trackedGame, 0, len(m.tracked))
	otherGames := make([]*trackedGame, 0, len(m.tracked))
	for _, tg := range m.tracked {
		if tg.state.Completed {
			continue
		}
		if tg.state.State == "in" || tg.state.Status == "in_progress" {
			liveGames = append(liveGames, tg)
			continue
		}
		otherGames = append(otherGames, tg)
	}
	m.mu.RUnlock()

	games := append(liveGames, otherGames...)
	for _, tg := range games {
		func() {
			defer recoverLog(fmt.Sprintf("poll %s:%s", tg.sport, tg.id))
			state, events, err := m.pollGame(ctx, tg)
			if err != nil {
				log.Printf("[espn] %s %s poll error: %v", tg.sport, tg.id, err)
				return
			}
			wasCompleted := tg.state.Completed
			m.mu.Lock()
			tg.state = state
			tg.allPlays = append(tg.allPlays, events...)
			m.mu.Unlock()
			for _, event := range events {
				if m.eventHandler != nil {
					m.eventHandler(event)
				}
			}
			if state.Completed && !wasCompleted && m.completionHandler != nil {
				m.mu.RLock()
				plays := make([]game.PlayEvent, len(tg.allPlays))
				copy(plays, tg.allPlays)
				m.mu.RUnlock()
				m.completionHandler(tg.id, tg.sport, state, plays)
			}
		}()
	}
}

func (m *Manager) fetchScoreboard(ctx context.Context, sport game.Sport) ([]string, error) {
	switch sport {
	case game.SportNBA:
		return FetchNBAScoreboard(ctx, m.client)
	case game.SportMLB:
		return FetchMLBScoreboard(ctx, m.client)
	case game.SportGolf:
		return FetchGolfScoreboard(ctx, m.client)
	default:
		return nil, fmt.Errorf("unsupported sport %s", sport)
	}
}

func (m *Manager) pollGame(ctx context.Context, tg *trackedGame) (game.GameState, []game.PlayEvent, error) {
	switch tg.sport {
	case game.SportNBA:
		return PollNBAGame(ctx, m.client, tg.id, tg.seen)
	case game.SportMLB:
		return PollMLBGame(ctx, m.client, tg.id, tg.seen)
	case game.SportGolf:
		return PollGolfGame(ctx, m.client, tg.id, tg.seen)
	default:
		return game.GameState{}, nil, fmt.Errorf("unsupported sport %s", tg.sport)
	}
}

func trackedKey(sport game.Sport, id string) string {
	return fmt.Sprintf("%s:%s", sport, id)
}

func recoverLog(scope string) {
	if r := recover(); r != nil {
		log.Printf("[espn] recovered panic in %s: %v", scope, r)
	}
}
