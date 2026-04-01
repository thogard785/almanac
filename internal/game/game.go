package game

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/almanac/espn-shots/internal/espn"
)

// GameState holds the current state of a tracked game.
type GameState struct {
	GameID    string `json:"game_id"`
	Sport     Sport  `json:"sport"`
	Status    string `json:"status"`
	State     string `json:"state"`
	Detail    string `json:"detail"`
	StartTime string `json:"start_time"`
	HomeTeam  string `json:"home_team"`
	AwayTeam  string `json:"away_team"`
	HomeScore string `json:"home_score"`
	AwayScore string `json:"away_score"`
	Completed bool   `json:"completed"`
}

// EventCallback is called when new sport events are discovered.
type EventCallback func(event SportEvent)

// Game represents a single tracked game/event with its own poll loop.
type Game struct {
	mu           sync.RWMutex
	gameID       string
	sport        Sport
	state        *GameState
	seen         map[string]struct{}
	pollInterval time.Duration
	client       *espn.Client
	onEvent      EventCallback
	cancel       context.CancelFunc
}

// NewGame creates a Game for the given sport and game ID.
func NewGame(gameID string, sport Sport, pollInterval time.Duration, client *espn.Client, onEvent EventCallback) *Game {
	return &Game{
		gameID:       gameID,
		sport:        sport,
		state:        &GameState{GameID: gameID, Sport: sport},
		seen:         make(map[string]struct{}),
		pollInterval: pollInterval,
		client:       client,
		onEvent:      onEvent,
	}
}

// ID returns the game ID.
func (g *Game) ID() string { return g.gameID }

// Sport returns the sport type.
func (g *Game) SportType() Sport { return g.sport }

// State returns a snapshot of the current game state.
func (g *Game) State() GameState {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.state == nil {
		return GameState{GameID: g.gameID, Sport: g.sport}
	}
	return *g.state
}

// IsCompleted returns true if the game is finished.
func (g *Game) IsCompleted() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.state != nil && g.state.Completed
}

// Run starts the poll loop for this game. Blocks until ctx is cancelled.
func (g *Game) Run(ctx context.Context) {
	ctx, g.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(g.pollInterval)
	defer ticker.Stop()

	log.Printf("[game:%s:%s] starting poll loop", g.sport, g.gameID)

	// Initial poll
	if err := g.poll(ctx); err != nil {
		log.Printf("[game:%s:%s] initial poll error: %v", g.sport, g.gameID, err)
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("[game:%s:%s] stopping", g.sport, g.gameID)
			return
		case <-ticker.C:
			if err := g.poll(ctx); err != nil {
				log.Printf("[game:%s:%s] poll error: %v", g.sport, g.gameID, err)
			}
			if g.IsCompleted() {
				log.Printf("[game:%s:%s] game completed, stopping poll", g.sport, g.gameID)
				return
			}
		}
	}
}

// Stop cancels the game's poll loop.
func (g *Game) Stop() {
	if g.cancel != nil {
		g.cancel()
	}
}

func (g *Game) poll(ctx context.Context) error {
	switch g.sport {
	case SportNBA:
		return g.pollNBA(ctx)
	case SportMLB:
		return g.pollMLB(ctx)
	case SportPGA:
		return g.pollGolf(ctx)
	default:
		return fmt.Errorf("unsupported sport: %s", g.sport)
	}
}

func (g *Game) pollNBA(ctx context.Context) error {
	parser := espn.NewNBAParser(g.client)

	g.mu.RLock()
	seenCopy := make(map[string]struct{}, len(g.seen))
	for k, v := range g.seen {
		seenCopy[k] = v
	}
	g.mu.RUnlock()

	shots, info, err := parser.FetchShots(ctx, g.gameID, seenCopy)
	if err != nil {
		return err
	}

	if info != nil {
		g.mu.Lock()
		g.state = &GameState{
			GameID:    g.gameID,
			Sport:     SportNBA,
			Status:    info.Status,
			State:     info.State,
			Detail:    info.Detail,
			StartTime: info.StartTime,
			HomeTeam:  info.HomeTeam,
			AwayTeam:  info.AwayTeam,
			HomeScore: info.HomeScore,
			AwayScore: info.AwayScore,
			Completed: info.Completed,
		}
		g.mu.Unlock()
	}

	for i := range shots {
		shots[i].TimestampNS = time.Now().UnixNano()
		g.mu.Lock()
		g.seen[shots[i].EventID] = struct{}{}
		g.mu.Unlock()
		if g.onEvent != nil {
			g.onEvent(&NBASportEvent{ShotEvent: shots[i]})
		}
	}
	return nil
}

func (g *Game) pollMLB(ctx context.Context) error {
	parser := espn.NewMLBParser(g.client)

	g.mu.RLock()
	seenCopy := make(map[string]struct{}, len(g.seen))
	for k, v := range g.seen {
		seenCopy[k] = v
	}
	g.mu.RUnlock()

	pitches, info, err := parser.FetchPitches(ctx, g.gameID, seenCopy)
	if err != nil {
		return err
	}

	if info != nil {
		g.mu.Lock()
		g.state = &GameState{
			GameID:    g.gameID,
			Sport:     SportMLB,
			Status:    info.Status,
			State:     info.State,
			Detail:    info.Detail,
			StartTime: info.StartTime,
			HomeTeam:  info.HomeTeam,
			AwayTeam:  info.AwayTeam,
			HomeScore: info.HomeScore,
			AwayScore: info.AwayScore,
			Completed: info.Completed,
		}
		g.mu.Unlock()
	}

	for i := range pitches {
		pitches[i].TimestampNS = time.Now().UnixNano()
		g.mu.Lock()
		g.seen[pitches[i].EventID] = struct{}{}
		g.mu.Unlock()
		if g.onEvent != nil {
			g.onEvent(&MLBSportEvent{PitchEvent: pitches[i]})
		}
	}
	return nil
}

func (g *Game) pollGolf(ctx context.Context) error {
	parser := espn.NewGolfParser(g.client)

	g.mu.RLock()
	seenCopy := make(map[string]struct{}, len(g.seen))
	for k, v := range g.seen {
		seenCopy[k] = v
	}
	g.mu.RUnlock()

	shots, info, err := parser.FetchShots(ctx, g.gameID, seenCopy)
	if err != nil {
		return err
	}

	if info != nil {
		g.mu.Lock()
		g.state = &GameState{
			GameID:    g.gameID,
			Sport:     SportPGA,
			Status:    info.Status,
			State:     info.State,
			Detail:    info.Detail,
			StartTime: info.StartTime,
			Completed: info.Completed,
		}
		g.mu.Unlock()
	}

	for i := range shots {
		shots[i].TimestampNS = time.Now().UnixNano()
		g.mu.Lock()
		g.seen[shots[i].EventID] = struct{}{}
		g.mu.Unlock()
		if g.onEvent != nil {
			g.onEvent(&GolfSportEvent{GolfShotEvent: shots[i]})
		}
	}
	return nil
}
