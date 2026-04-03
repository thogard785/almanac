package sim

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/almanac/espn-shots/internal/bet"
	"github.com/almanac/espn-shots/internal/game"
)

const (
	InitialSimBalance  = 100.0
	SimBetDir          = "data/sim_bets"
	SimGameDir         = "data/sim_games"
	DefaultEventDelay  = 15 * time.Second
	DefaultReleaseTick = 250 * time.Millisecond
)

// Config controls the simulation lane's isolated runtime behavior.
type Config struct {
	StoreDir    string
	GameDir     string
	EventDelay  time.Duration
	ReleaseTick time.Duration
}

// Manager coordinates all simulation-mode state: isolated balances, a separate
// bet engine, a separate assumed-possession tracker, and a delayed mirror of
// live ESPN games. There is exactly one Manager per server.
type Manager struct {
	Balances *SimBalanceProvider
	Engine   *bet.Engine
	Store    *bet.Store

	tracker     *game.AssumedPossessionTracker
	delay       time.Duration
	releaseTick time.Duration

	mu      sync.RWMutex
	games   map[string]*mirroredGame
	gameDir string

	// EventHandler is called for each delayed simulation play event (wired by
	// main.go to broadcast to sim connections and feed later UI work).
	EventHandler func(game.PlayEvent)
}

type mirroredGame struct {
	sourceGameID string
	sourceState  game.GameState
	simState     game.GameState
	seenEvents   map[string]struct{}
	pending      []queuedEvent
	sequence     int
}

type queuedEvent struct {
	sourceEvent game.PlayEvent
	sourceState game.GameState
	observedAt  time.Time
	releaseAt   time.Time
	sequence    int
}

// NewManager creates the simulation manager with fully isolated state.
func NewManager() (*Manager, error) {
	return NewManagerWithConfig(Config{
		StoreDir:    SimBetDir,
		GameDir:     SimGameDir,
		EventDelay:  DefaultEventDelay,
		ReleaseTick: DefaultReleaseTick,
	})
}

// NewManagerWithConfig is used by tests and future runtime tuning.
func NewManagerWithConfig(cfg Config) (*Manager, error) {
	if cfg.StoreDir == "" {
		cfg.StoreDir = SimBetDir
	}
	if cfg.GameDir == "" {
		cfg.GameDir = SimGameDir
	}
	if cfg.EventDelay < 0 {
		cfg.EventDelay = 0
	}
	if cfg.ReleaseTick <= 0 {
		cfg.ReleaseTick = DefaultReleaseTick
	}

	store, err := bet.NewStore(cfg.StoreDir)
	if err != nil {
		return nil, err
	}
	bp := NewSimBalanceProvider()
	engine := bet.NewEngineWithBalance(store, bp).EnableNextRoundResolution()
	return &Manager{
		Balances:    bp,
		Engine:      engine,
		Store:       store,
		tracker:     game.NewAssumedPossessionTracker(),
		delay:       cfg.EventDelay,
		releaseTick: cfg.ReleaseTick,
		games:       make(map[string]*mirroredGame),
		gameDir:     cfg.GameDir,
	}, nil
}

// Start runs the delayed release loop for mirrored live events.
func (m *Manager) Start(ctx context.Context) {
	go m.releaseLoop(ctx)
}

func (m *Manager) releaseLoop(ctx context.Context) {
	ticker := time.NewTicker(m.releaseTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.releaseDueEvents()
		}
	}
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

// SyncLiveGames refreshes the source-game roster without leaking live state
// directly into the simulation lane. Scores, rounds, and assumed-possession
// state only advance when delayed events are released through ObserveLiveEvent.
func (m *Manager) SyncLiveGames(states []game.GameState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, state := range states {
		if state.GameID == "" || state.Simulation {
			continue
		}
		mirror := m.ensureGameLocked(state)
		mirror.sourceState = state
		updateShellState(&mirror.simState, state)
		if state.Completed && len(mirror.pending) == 0 && mirror.sequence == 0 {
			mirror.simState.Status = state.Status
			mirror.simState.State = state.State
			mirror.simState.Completed = true
		}
	}
}

// ObserveLiveEvent records a real-game play plus the live snapshot seen at the
// time, then schedules delayed release into the isolated simulation lane.
func (m *Manager) ObserveLiveEvent(state game.GameState, event game.PlayEvent) {
	if state.GameID == "" || event.GameID == "" || state.GameID != event.GameID {
		return
	}
	observedAt := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	mirror := m.ensureGameLocked(state)
	mirror.sourceState = state
	updateShellState(&mirror.simState, state)
	if _, exists := mirror.seenEvents[event.PlayID]; exists {
		return
	}
	mirror.seenEvents[event.PlayID] = struct{}{}
	mirror.sequence++
	mirror.pending = append(mirror.pending, queuedEvent{
		sourceEvent: event,
		sourceState: state,
		observedAt:  observedAt,
		releaseAt:   observedAt.Add(m.delay),
		sequence:    mirror.sequence,
	})
}

// Games returns the currently mirrored simulation game views.
func (m *Manager) Games() []game.GameState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]game.GameState, 0, len(m.games))
	for _, mirror := range m.games {
		if mirror.simState.GameID == "" {
			continue
		}
		out = append(out, mirror.simState)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Sport == out[j].Sport {
			return out[i].GameID < out[j].GameID
		}
		return out[i].Sport < out[j].Sport
	})
	return out
}

// Game returns the isolated simulation view for the requested sim game.
func (m *Manager) Game(gameID string) (game.GameState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mirror, ok := m.games[gameID]
	if !ok {
		return game.GameState{}, false
	}
	return mirror.simState, true
}

// GameRadii returns sim game radii in the same wire format as the live lane.
func (m *Manager) GameRadii() map[string]float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]float64, len(m.games))
	for gameID := range m.games {
		out[gameID] = bet.WinRadius
	}
	return out
}

// Admit evaluates a simulation bet against the simulation lane's isolated
// assumed-possession tracker.
func (m *Manager) Admit(gameID, roundID string, betTimestamp int64, expiryWindow time.Duration) (game.BetAdmission, bool) {
	state, ok := m.Game(gameID)
	if !ok {
		return game.BetAdmission{}, false
	}
	return m.tracker.Admit(state, roundID, betTimestamp, expiryWindow), true
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

// SaveCompletedGame remains available for archival replay tooling even though
// item 8's runtime simulation path now mirrors live games instead of booting
// exclusively from a completed-game replayer.
func (m *Manager) SaveCompletedGame(gameID string, sport game.Sport, state game.GameState, plays []game.PlayEvent) {
	if sport != game.SportNBA {
		return // only NBA games are archived for replay right now
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

// Stop currently exists for interface symmetry with the old replayer-backed
// manager. The live-mirror release loop is context-driven, so Stop is a no-op.
func (m *Manager) Stop() {}

// GameDir exposes the archival game directory for tests.
func (m *Manager) GameDir() string { return m.gameDir }

func (m *Manager) ensureGameLocked(state game.GameState) *mirroredGame {
	simID := SimGameID(state.GameID)
	mirror := m.games[simID]
	if mirror == nil {
		mirror = &mirroredGame{
			sourceGameID: state.GameID,
			seenEvents:   make(map[string]struct{}),
			simState: game.GameState{
				GameID:     simID,
				Simulation: true,
				Tracked:    true,
			},
		}
		m.games[simID] = mirror
	}
	updateShellState(&mirror.simState, state)
	mirror.sourceState = state
	return mirror
}

func updateShellState(simState *game.GameState, sourceState game.GameState) {
	if simState == nil {
		return
	}
	simState.GameID = SimGameID(sourceState.GameID)
	simState.Sport = sourceState.Sport
	simState.Status = sourceState.Status
	simState.State = sourceState.State
	simState.Detail = sourceState.Detail
	simState.StartTime = sourceState.StartTime
	simState.Home = sourceState.Home
	simState.Away = sourceState.Away
	simState.Tracked = sourceState.Tracked
	simState.Simulation = true
}

func (m *Manager) releaseDueEvents() {
	now := time.Now().UTC()
	type emission struct {
		event game.PlayEvent
	}
	var emissions []emission

	m.mu.Lock()
	for _, mirror := range m.games {
		for len(mirror.pending) > 0 {
			next := mirror.pending[0]
			if next.releaseAt.After(now) {
				break
			}
			mirror.pending = mirror.pending[1:]

			emitAt := now
			applyReleasedState(&mirror.simState, next.sourceState)
			simEvent := buildSimEvent(next.sourceEvent, emitAt)
			replayLatency := buildReplayLatency(next, emitAt)
			snapshot := game.InferSimulationAssumedPossession(mirror.simState, simEvent, replayLatency)
			if snapshot != nil {
				mirror.simState.ContractVersion = game.ContractVersionAssumedPossessionV1
				mirror.simState.AssumedPossession = snapshot
			}
			m.tracker.OnPlay(simEvent)
			if next.sourceState.Completed && len(mirror.pending) == 0 {
				mirror.simState.Completed = true
			}
			emissions = append(emissions, emission{event: simEvent})
		}
	}
	m.mu.Unlock()

	for _, item := range emissions {
		m.Engine.OnPlayEvent(item.event)
		if m.EventHandler != nil {
			m.EventHandler(item.event)
		}
	}
}

func applyReleasedState(simState *game.GameState, sourceState game.GameState) {
	updateShellState(simState, sourceState)
	simState.HomeScore = sourceState.HomeScore
	simState.AwayScore = sourceState.AwayScore
	simState.Period = sourceState.Period
	simState.Clock = sourceState.Clock
	simState.Possession = sourceState.Possession
	simState.Completed = sourceState.Completed
}

func buildSimEvent(source game.PlayEvent, emitAt time.Time) game.PlayEvent {
	return game.PlayEvent{
		GameID:    SimGameID(source.GameID),
		PlayID:    SimPlayID(source.GameID, source.PlayID),
		Sport:     source.Sport,
		Timestamp: emitAt,
		Location:  source.Location,
		EventData: source.EventData,
	}
}

func buildReplayLatency(next queuedEvent, emitAt time.Time) *game.ReplayLatencyMeta {
	meta := &game.ReplayLatencyMeta{
		ReplaySourceGameID: next.sourceEvent.GameID,
		ReplaySequence:     next.sequence,
		ObservedAt:         next.observedAt.UTC().Format(time.RFC3339Nano),
		EmittedAt:          emitAt.UTC().Format(time.RFC3339Nano),
		ReplayOffsetMs:     next.releaseAt.Sub(next.observedAt).Milliseconds(),
		Synthetic:          true,
	}
	if !next.sourceEvent.Timestamp.IsZero() {
		meta.SourceEventTimestamp = next.sourceEvent.Timestamp.UTC().Format(time.RFC3339Nano)
		meta.FeedLagMs = next.observedAt.Sub(next.sourceEvent.Timestamp).Milliseconds()
	}
	return meta
}
