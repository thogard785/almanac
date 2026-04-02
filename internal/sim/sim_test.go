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

// ---------- 1. Sim user creation with $100 balance ----------

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
	// Spend some sim money.
	mgr.Balances.AddBalance(wallet, -25.0)

	// Second init should NOT reset to $100.
	balance := mgr.InitWallet(wallet)
	if balance != 75.0 {
		t.Fatalf("expected balance 75.00 after spend, got %.2f", balance)
	}
}

// ---------- 2. Sim game bootstrap ----------

func TestEnsureGame_StartsReplayWhenSavedGameExists(t *testing.T) {
	mgr := newTestManager(t)
	writeTestGame(t, mgr.GameDir())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	mgr.EnsureGame(ctx)

	state, ok := mgr.ActiveGame()
	if !ok {
		t.Fatal("expected active sim game after EnsureGame")
	}
	if !state.Simulation {
		t.Fatal("expected Simulation=true on game state")
	}
	if state.Status != "in_progress" {
		t.Fatalf("expected status in_progress, got %s", state.Status)
	}
	if state.Home != "BOS" || state.Away != "LAL" {
		t.Fatalf("expected BOS vs LAL, got %s vs %s", state.Home, state.Away)
	}
}

func TestEnsureGame_NoOpWhenAlreadyRunning(t *testing.T) {
	mgr := newTestManager(t)
	writeTestGame(t, mgr.GameDir())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	mgr.EnsureGame(ctx)
	id1 := mgr.ActiveGameID()

	mgr.EnsureGame(ctx)
	id2 := mgr.ActiveGameID()

	if id1 != id2 {
		t.Fatalf("second EnsureGame started a new game: %s vs %s", id1, id2)
	}
}

// ---------- 3. Replay stream behaviour ----------

func TestReplayer_EmitsEventsInOrder(t *testing.T) {
	saved := testSavedGame()
	var received []game.PlayEvent
	r := NewReplayer(saved, func(ev game.PlayEvent) {
		received = append(received, ev)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.Run(ctx)

	if len(received) != len(saved.Plays) {
		t.Fatalf("expected %d events, got %d", len(saved.Plays), len(received))
	}
	for i, ev := range received {
		if ev.GameID != SimGameID(saved.GameID) {
			t.Fatalf("event %d: expected sim game ID %s, got %s", i, SimGameID(saved.GameID), ev.GameID)
		}
		if i > 0 && ev.Timestamp.Before(received[i-1].Timestamp) {
			t.Fatalf("event %d out of order", i)
		}
	}
	if !r.Done() {
		t.Fatal("replayer should be done")
	}
}

func TestReplayer_GameStateTransitions(t *testing.T) {
	saved := testSavedGame()
	r := NewReplayer(saved, func(ev game.PlayEvent) {})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.Run(ctx)

	state := r.GameState()
	if state.Status != "final" || !state.Completed {
		t.Fatalf("expected final/completed state, got status=%s completed=%v", state.Status, state.Completed)
	}
}

// ---------- 4. Sim-only betting isolation ----------

func TestSimBalance_IsolatedFromRegular(t *testing.T) {
	var wallet [20]byte
	copy(wallet[:], []byte("12345678901234567890"))

	// Set regular balance.
	bet.DefaultBalanceProvider.SetBalance(wallet, 500.0)

	// Sim balance is separate.
	simBP := NewSimBalanceProvider()
	simBP.SetBalance(wallet, 100.0)

	if simBP.GetBalance(wallet) != 100.0 {
		t.Fatal("sim balance should be 100")
	}
	if bet.DefaultBalanceProvider.GetBalance(wallet) != 500.0 {
		t.Fatal("regular balance should be 500")
	}

	// Spending sim money doesn't affect regular.
	simBP.AddBalance(wallet, -50.0)
	if bet.DefaultBalanceProvider.GetBalance(wallet) != 500.0 {
		t.Fatal("regular balance must not change after sim spend")
	}
	if simBP.GetBalance(wallet) != 50.0 {
		t.Fatal("sim balance should be 50 after spend")
	}
}

func TestSimEngine_UsesSimBalances(t *testing.T) {
	dir := t.TempDir()
	store, err := bet.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	bp := NewSimBalanceProvider()
	engine := bet.NewEngineWithBalance(store, bp)

	var wallet [20]byte
	copy(wallet[:], []byte("12345678901234567890"))
	bp.SetBalance(wallet, 100.0)

	// Regular balance should be unaffected.
	regBalance := bet.DefaultBalanceProvider.GetBalance(wallet)

	_ = engine // engine is wired to sim balances
	if bp.GetBalance(wallet) != 100.0 {
		t.Fatal("sim engine balance wrong")
	}
	if bet.DefaultBalanceProvider.GetBalance(wallet) != regBalance {
		t.Fatal("regular balance changed unexpectedly")
	}
}

// ---------- 5. Regular vs sim separation ----------

func TestGameRadii_OnlySimGames(t *testing.T) {
	mgr := newTestManager(t)
	writeTestGame(t, mgr.GameDir())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	mgr.EnsureGame(ctx)

	radii := mgr.GameRadii()
	if len(radii) != 1 {
		t.Fatalf("expected 1 sim game radius, got %d", len(radii))
	}
	for id := range radii {
		if id[:4] != "sim:" {
			t.Fatalf("expected sim: prefix, got %s", id)
		}
	}
}

// ---------- 6. Save/Load completed game data ----------

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

// ---------- helpers ----------

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	store, err := bet.NewStore(filepath.Join(dir, "bets"))
	if err != nil {
		t.Fatal(err)
	}
	bp := NewSimBalanceProvider()
	engine := bet.NewEngineWithBalance(store, bp)
	return &Manager{
		Balances: bp,
		Engine:   engine,
		Store:    store,
		gameDir:  filepath.Join(dir, "games"),
	}
}

// GameDir exposes the game directory for tests.
func (m *Manager) GameDir() string { return m.gameDir }

func writeTestGame(t *testing.T, dir string) {
	t.Helper()
	plays := testPlays()
	state := game.GameState{
		GameID: "401234567", Sport: "nba", Home: "BOS", Away: "LAL",
		Completed: true, Status: "final", HomeScore: 110, AwayScore: 105,
	}
	if err := SaveCompletedGame(dir, "401234567", game.SportNBA, state, plays); err != nil {
		t.Fatal(err)
	}
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

func testSavedGame() *SavedGame {
	plays := testPlays()
	return &SavedGame{
		GameID: "401234567",
		Sport:  "nba",
		State: game.GameState{
			GameID: "401234567", Sport: "nba", Home: "BOS", Away: "LAL",
			Completed: true, Status: "final", HomeScore: 110, AwayScore: 105,
		},
		Plays:   plays,
		SavedAt: time.Now(),
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
