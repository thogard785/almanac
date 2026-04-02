package sim

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/almanac/espn-shots/internal/bet"
	"github.com/almanac/espn-shots/internal/game"
)

const (
	InitialSimBalance = 100.0
	SimBetDir         = "data/sim_bets"
	SimGameDir        = "data/sim_games"
)

// Manager coordinates all simulation-mode state: isolated balances, a separate
// bet engine, and the game replayer. There is exactly one Manager per server.
type Manager struct {
	Balances *SimBalanceProvider
	Engine   *bet.Engine
	Store    *bet.Store

	mu       sync.RWMutex
	replayer *Replayer
	cancel   context.CancelFunc
	gameDir  string

	// EventHandler is called for each replayed play event (wired by main.go
	// to broadcast to sim connections and feed the sim bet engine).
	EventHandler func(game.PlayEvent)
}

// NewManager creates the simulation manager with fully isolated state.
func NewManager() (*Manager, error) {
	store, err := bet.NewStore(SimBetDir)
	if err != nil {
		return nil, err
	}
	bp := NewSimBalanceProvider()
	engine := bet.NewEngineWithBalance(store, bp)
	return &Manager{
		Balances: bp,
		Engine:   engine,
		Store:    store,
		gameDir:  SimGameDir,
	}, nil
}

// InitWallet ensures the wallet has a simulation balance, granting the
// initial $100 if this is the first time.
func (m *Manager) InitWallet(wallet [20]byte) float64 {
	if !m.Balances.HasWallet(wallet) {
		m.Balances.SetBalance(wallet, InitialSimBalance)
		log.Printf("[sim] initialized wallet %x with $%.2f", wallet, InitialSimBalance)
	}
	return m.Balances.GetBalance(wallet)
}

// EnsureGame starts a simulation game if none is currently active.
// It loads the most recently saved completed game and starts replaying it.
func (m *Manager) EnsureGame(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.replayer != nil && !m.replayer.Done() {
		return // already running
	}
	saved, err := LoadLatestGame(m.gameDir)
	if err != nil {
		log.Printf("[sim] no saved game available for replay: %v", err)
		return
	}
	replayCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	r := NewReplayer(saved, func(ev game.PlayEvent) {
		m.Engine.OnPlayEvent(ev)
		if m.EventHandler != nil {
			m.EventHandler(ev)
		}
	})
	m.replayer = r
	go r.Run(replayCtx)
	log.Printf("[sim] started replay game %s", r.GameID())
}

// ActiveGame returns the current simulation game state, if any.
func (m *Manager) ActiveGame() (game.GameState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.replayer == nil {
		return game.GameState{}, false
	}
	return m.replayer.GameState(), true
}

// ActiveGameID returns the sim game ID, or empty if none.
func (m *Manager) ActiveGameID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.replayer == nil {
		return ""
	}
	return m.replayer.GameID()
}

// GameRadii returns the sim game radii map (matching regular-mode format).
func (m *Manager) GameRadii() map[string]float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]float64)
	if m.replayer != nil && !m.replayer.Done() {
		out[m.replayer.GameID()] = bet.WinRadius
	}
	return out
}

// CurrentBalance returns the sim balance for a wallet.
func (m *Manager) CurrentBalance(wallet [20]byte) float64 {
	return m.Balances.GetBalance(wallet)
}

// NextNonce returns the next available nonce for sim bets.
func (m *Manager) NextNonce(wallet [20]byte) uint64 {
	return m.Engine.NextNonce(wallet)
}

// BetHistory returns sim bet history for a wallet.
func (m *Manager) BetHistory(wallet [20]byte, since time.Time) []bet.SignInAckBetHistory {
	return m.Engine.BetHistory(wallet, since)
}

// SaveCompletedGame is the CompletionHandler wired to the ESPN manager.
// It saves NBA game data for future replay.
func (m *Manager) SaveCompletedGame(gameID string, sport game.Sport, state game.GameState, plays []game.PlayEvent) {
	if sport != game.SportNBA {
		return // only NBA games are replayable for now
	}
	if len(plays) < 10 {
		log.Printf("[sim] skipping save of %s %s: only %d plays", sport, gameID, len(plays))
		return
	}
	if err := SaveCompletedGame(m.gameDir, gameID, sport, state, plays); err != nil {
		log.Printf("[sim] failed to save completed game %s: %v", gameID, err)
	} else {
		log.Printf("[sim] saved completed game %s (%d plays)", gameID, len(plays))
	}
}

// Stop cancels any running replay.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}
