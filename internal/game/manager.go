package game

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/almanac/espn-shots/internal/espn"
)

// Manager discovers and tracks all live games across sports.
type Manager struct {
	mu            sync.RWMutex
	games         map[string]*Game // key: "sport:gameID"
	client        *espn.Client
	pollInterval  time.Duration
	sports        []Sport
	onEvent       EventCallback
	discoveryDone chan struct{} // closed after first discovery
}

// NewManager creates a new game manager.
func NewManager(client *espn.Client, pollInterval time.Duration, sports []Sport, onEvent EventCallback) *Manager {
	return &Manager{
		games:         make(map[string]*Game),
		client:        client,
		pollInterval:  pollInterval,
		sports:        sports,
		onEvent:       onEvent,
		discoveryDone: make(chan struct{}),
	}
}

// Run starts the discovery loop. Blocks until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	log.Printf("[manager] starting discovery loop for sports: %v", m.sports)

	// Initial discovery
	m.discover(ctx)
	close(m.discoveryDone)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			log.Printf("[manager] stopped")
			return
		case <-ticker.C:
			m.discover(ctx)
			m.cleanupCompleted()
		}
	}
}

// Games returns a snapshot of all tracked game states.
func (m *Manager) Games() []GameState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	states := make([]GameState, 0, len(m.games))
	for _, g := range m.games {
		states = append(states, g.State())
	}
	return states
}

// Game returns a specific game by ID.
func (m *Manager) Game(gameID string) *Game {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, g := range m.games {
		if g.gameID == gameID {
			return g
		}
	}
	return nil
}

// WaitForDiscovery blocks until the first discovery cycle is complete.
func (m *Manager) WaitForDiscovery(ctx context.Context) {
	select {
	case <-m.discoveryDone:
	case <-ctx.Done():
	}
}

func (m *Manager) discover(ctx context.Context) {
	for _, sport := range m.sports {
		if ctx.Err() != nil {
			return
		}
		events, err := m.fetchScoreboard(ctx, sport)
		if err != nil {
			log.Printf("[manager] %s scoreboard error: %v", sport, err)
			continue
		}
		for _, ev := range events {
			state := ev.Status.Type.State
			// Track games that are live ("in") or recently completed ("post")
			if state != "in" && state != "post" {
				continue
			}
			key := string(sport) + ":" + ev.ID
			m.mu.RLock()
			_, exists := m.games[key]
			m.mu.RUnlock()
			if exists {
				continue
			}
			g := NewGame(ev.ID, sport, m.pollInterval, m.client, m.onEvent)
			m.mu.Lock()
			m.games[key] = g
			m.mu.Unlock()
			log.Printf("[manager] discovered %s game %s: %s", sport, ev.ID, ev.ShortName)
			go g.Run(ctx)
		}
	}
}

func (m *Manager) fetchScoreboard(ctx context.Context, sport Sport) ([]espn.ScoreboardEvent, error) {
	switch sport {
	case SportNBA:
		return espn.NewNBAParser(m.client).FetchScoreboard(ctx)
	case SportMLB:
		return espn.NewMLBParser(m.client).FetchScoreboard(ctx)
	case SportPGA:
		return espn.NewGolfParser(m.client).FetchScoreboard(ctx)
	default:
		return nil, nil
	}
}

func (m *Manager) cleanupCompleted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, g := range m.games {
		if g.IsCompleted() {
			log.Printf("[manager] removing completed game %s", key)
			delete(m.games, key)
		}
	}
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, g := range m.games {
		g.Stop()
	}
}
