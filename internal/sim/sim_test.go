package sim

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/almanac/espn-shots/internal/bet"
	"github.com/almanac/espn-shots/internal/game"
)

func TestInitWallet_GrantsInitialBalance(t *testing.T) {
	mgr := newTestManager(t)

	var wallet [20]byte
	copy(wallet[:], []byte("12345678901234567890"))

	balance := mgr.InitWallet(wallet)
	if balance != InitialSimBalance {
		t.Fatalf("expected initial balance %.2f, got %.2f", InitialSimBalance, balance)
	}
}

func TestInitWallet_IdempotentOnSecondCall(t *testing.T) {
	mgr := newTestManager(t)

	var wallet [20]byte
	copy(wallet[:], []byte("12345678901234567890"))

	mgr.InitWallet(wallet)
	mgr.Balances.AddBalance(wallet, -25.0)

	balance := mgr.InitWallet(wallet)
	if balance != 75.0 {
		t.Fatalf("expected balance 75.00 after spend, got %.2f", balance)
	}
}

func TestSyncLiveGames_DoesNotLeakLiveScoreboardBeforeDelayedRelease(t *testing.T) {
	mgr := newTestManager(t)
	source := liveState()
	source.HomeScore = 88
	source.AwayScore = 90
	source.Period = "Q4"
	source.Clock = "02:11"
	source.Possession = "LAL"

	mgr.SyncLiveGames([]game.GameState{source})

	state, ok := mgr.Game(SimGameID(source.GameID))
	if !ok {
		t.Fatal("expected mirrored sim game")
	}
	if state.HomeScore != 0 || state.AwayScore != 0 {
		t.Fatalf("live scores leaked into sim shell: %+v", state)
	}
	if state.Period != "" || state.Clock != "" || state.Possession != "" {
		t.Fatalf("live round state leaked into sim shell: %+v", state)
	}
	if !state.Simulation {
		t.Fatal("expected simulation lane marker")
	}
}

func TestObserveLiveEvent_ReleasesDelayedIsolatedMirror(t *testing.T) {
	mgr := newTestManager(t)
	received := make(chan game.PlayEvent, 1)
	mgr.EventHandler = func(ev game.PlayEvent) {
		received <- ev
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	mgr.Start(ctx)

	source := liveState()
	source.HomeScore = 2
	source.AwayScore = 0
	source.Period = "Q1"
	source.Clock = "11:30"
	source.Possession = "LAL"
	event := liveEvent(source.GameID, "p1")

	mgr.SyncLiveGames([]game.GameState{source})
	mgr.ObserveLiveEvent(source, event)

	var simEvent game.PlayEvent
	select {
	case simEvent = <-received:
	case <-ctx.Done():
		t.Fatal("timed out waiting for delayed sim event")
	}

	if simEvent.GameID != SimGameID(source.GameID) {
		t.Fatalf("expected sim game ID %s, got %s", SimGameID(source.GameID), simEvent.GameID)
	}
	if simEvent.PlayID != SimPlayID(source.GameID, event.PlayID) {
		t.Fatalf("expected namespaced sim play ID, got %s", simEvent.PlayID)
	}

	state, ok := mgr.Game(SimGameID(source.GameID))
	if !ok {
		t.Fatal("expected mirrored game state")
	}
	if state.HomeScore != source.HomeScore || state.AwayScore != source.AwayScore {
		t.Fatalf("expected delayed scoreboard to match released source state, got %+v", state)
	}
	if state.AssumedPossession == nil {
		t.Fatal("expected assumed-possession contract on sim lane")
	}
	if state.AssumedPossession.Lane.Kind != game.LaneKindSimulation || !state.AssumedPossession.Lane.Isolated {
		t.Fatalf("unexpected lane metadata: %+v", state.AssumedPossession.Lane)
	}
	if state.AssumedPossession.BoundGameID != SimGameID(source.GameID) {
		t.Fatalf("expected bound sim game ID, got %s", state.AssumedPossession.BoundGameID)
	}
	if state.AssumedPossession.ReplayLatency == nil {
		t.Fatal("expected replay latency metadata")
	}
	if state.AssumedPossession.ReplayLatency.ReplaySourceGameID != source.GameID {
		t.Fatalf("expected replay source %s, got %s", source.GameID, state.AssumedPossession.ReplayLatency.ReplaySourceGameID)
	}
	if state.AssumedPossession.ReplayLatency.ReplayOffsetMs <= 0 {
		t.Fatalf("expected positive replay offset, got %d", state.AssumedPossession.ReplayLatency.ReplayOffsetMs)
	}
}

func TestSimAdmit_UsesIsolatedTrackerAndBinding(t *testing.T) {
	mgr := newTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	mgr.Start(ctx)

	source := liveState()
	source.HomeScore = 2
	source.Possession = "LAL"
	event := liveEvent(source.GameID, "p1")

	mgr.SyncLiveGames([]game.GameState{source})
	mgr.ObserveLiveEvent(source, event)
	waitForRound(t, mgr, SimGameID(source.GameID), SimPlayID(source.GameID, event.PlayID))

	decision, ok := mgr.Admit(SimGameID(source.GameID), SimPlayID(source.GameID, event.PlayID), time.Now().Unix(), 30*time.Second)
	if !ok {
		t.Fatal("expected simulation game for admit")
	}
	if !decision.Accepted {
		t.Fatalf("expected accepted admit, got %+v", decision)
	}
	if decision.Binding == nil {
		t.Fatal("expected binding")
	}
	if decision.Binding.GameID != SimGameID(source.GameID) {
		t.Fatalf("expected bound sim game ID, got %s", decision.Binding.GameID)
	}
	if decision.Binding.Lane.Kind != game.LaneKindSimulation || !decision.Binding.Lane.Isolated {
		t.Fatalf("unexpected binding lane: %+v", decision.Binding.Lane)
	}
}

func TestSimBalance_IsolatedFromRegular(t *testing.T) {
	var wallet [20]byte
	copy(wallet[:], []byte("12345678901234567890"))

	bet.DefaultBalanceProvider.SetBalance(wallet, 500.0)

	simBP := NewSimBalanceProvider()
	simBP.SetBalance(wallet, 100.0)

	if simBP.GetBalance(wallet) != 100.0 {
		t.Fatal("sim balance should be 100")
	}
	if bet.DefaultBalanceProvider.GetBalance(wallet) != 500.0 {
		t.Fatal("regular balance should be 500")
	}

	simBP.AddBalance(wallet, -50.0)
	if bet.DefaultBalanceProvider.GetBalance(wallet) != 500.0 {
		t.Fatal("regular balance must not change after sim spend")
	}
	if simBP.GetBalance(wallet) != 50.0 {
		t.Fatal("sim balance should be 50 after spend")
	}
}

func TestSaveAndLoadCompletedGame(t *testing.T) {
	dir := t.TempDir()
	plays := testPlays()
	state := game.GameState{
		GameID: "401234567", Sport: "nba", Home: "BOS", Away: "LAL",
		Completed: true, Status: "final",
	}
	err := SaveCompletedGame(dir, "401234567", game.SportNBA, state, plays)
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadLatestGame(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GameID != "401234567" {
		t.Fatalf("wrong game ID: %s", loaded.GameID)
	}
	if len(loaded.Plays) != len(plays) {
		t.Fatalf("expected %d plays, got %d", len(plays), len(loaded.Plays))
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	mgr, err := NewManagerWithConfig(Config{
		StoreDir:    filepath.Join(dir, "bets"),
		GameDir:     filepath.Join(dir, "games"),
		EventDelay:  10 * time.Millisecond,
		ReleaseTick: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mgr
}

func liveState() game.GameState {
	return game.GameState{
		GameID:    "401234567",
		Sport:     "nba",
		Status:    "in_progress",
		State:     "in",
		StartTime: time.Now().UTC().Format(time.RFC3339),
		Home:      "BOS",
		Away:      "LAL",
		Tracked:   true,
	}
}

func liveEvent(gameID, playID string) game.PlayEvent {
	return game.PlayEvent{
		GameID:    gameID,
		PlayID:    playID,
		Sport:     "nba",
		Timestamp: time.Now().UTC().Add(-2 * time.Second),
		Location:  &game.Coord{X: 100, Y: 200},
		EventData: map[string]any{"period": "Q1", "clock": "11:30", "made": true, "shot_type": "2pt shot", "team": "BOS"},
	}
}

func waitForRound(t *testing.T, mgr *Manager, gameID, roundID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, ok := mgr.Game(gameID)
		if ok && state.AssumedPossession != nil && state.AssumedPossession.BoundRoundID == roundID {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for round %s on %s", roundID, gameID)
}

func testPlays() []game.PlayEvent {
	base := time.Now().Add(-10 * time.Second)
	return []game.PlayEvent{
		{
			GameID: "401234567", PlayID: "p1", Sport: "nba",
			Timestamp: base,
			Location:  &game.Coord{X: 100, Y: 200},
			EventData: map[string]any{"period": "Q1", "clock": "11:30", "made": true, "shot_type": "2pt shot", "team": "BOS"},
		},
		{
			GameID: "401234567", PlayID: "p2", Sport: "nba",
			Timestamp: base.Add(1 * time.Second),
			Location:  &game.Coord{X: 150, Y: 250},
			EventData: map[string]any{"period": "Q1", "clock": "10:45", "made": false, "shot_type": "3pt jump shot", "team": "LAL"},
		},
		{
			GameID: "401234567", PlayID: "p3", Sport: "nba",
			Timestamp: base.Add(2 * time.Second),
			Location:  &game.Coord{X: 200, Y: 300},
			EventData: map[string]any{"period": "Q2", "clock": "5:00", "made": true, "shot_type": "3pt jump shot", "team": "LAL"},
		},
	}
}

func TestSaveCompletedGame_RejectsTooFewPlays(t *testing.T) {
	dir := t.TempDir()
	err := SaveCompletedGame(dir, "x", game.SportNBA, game.GameState{}, nil)
	if err == nil {
		t.Fatal("expected error for empty plays")
	}
}

func TestLoadLatestGame_ErrorOnEmptyDir(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadLatestGame(dir)
	if err == nil {
		t.Fatal("expected error on empty dir")
	}
}

func TestSimBalanceProvider_HasWallet(t *testing.T) {
	bp := NewSimBalanceProvider()
	var w [20]byte
	if bp.HasWallet(w) {
		t.Fatal("should not have wallet yet")
	}
	bp.SetBalance(w, 100)
	if !bp.HasWallet(w) {
		t.Fatal("should have wallet after set")
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
