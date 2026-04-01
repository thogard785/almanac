package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// ErrGameFinal is returned by Tracker.Run when the game reaches final status.
var ErrGameFinal = errors.New("game final")

const (
	summaryURLFmt = "https://site.api.espn.com/apis/site/v2/sports/basketball/nba/summary?event=%s"
	playsURLFmt   = "https://sports.core.api.espn.com/v2/sports/basketball/leagues/nba/events/%s/competitions/%s/plays?limit=1000"
)

func main() {
	cfg := loadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tracker, err := NewTracker(cfg)
	if err != nil {
		log.Fatalf("init tracker: %v", err)
	}

	// Start WebSocket hub + HTTP server
	hub := NewHub()
	updates := make(chan interface{}, 64)
	go hub.RunBroadcastLoop(ctx, updates)
	tracker.broadcastCh = updates

	// HTTP server on configurable port
	mux := http.NewServeMux()
	wsHandler := hub.HandleWS(tracker.GameStateMessage)
	mux.HandleFunc("/ws", wsHandler)
	mux.HandleFunc("/almanac/ws", wsHandler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	wsPort := os.Getenv("WS_PORT")
	if wsPort == "" {
		wsPort = "8090"
	}
	srv := &http.Server{
		Addr:    ":" + wsPort,
		Handler: mux,
	}
	go func() {
		log.Printf("[ws] starting WebSocket server on :%s", wsPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[ws] server error: %v", err)
		}
	}()

	log.Printf("tracking game=%s poll=%s output=%s", cfg.GameID, cfg.PollInterval, cfg.OutputPath)
	runErr := tracker.Run(ctx)
	switch {
	case errors.Is(runErr, ErrGameFinal):
		// Poller already flushed on game-final; keep WS server alive for
		// connected clients until an external signal arrives.
		log.Printf("[main] game final — poller stopped, WS server remains active")
		<-ctx.Done()
	case runErr != nil && !errors.Is(runErr, context.Canceled):
		log.Fatalf("run tracker: %v", runErr)
	default:
		// Context cancelled (signal) — flush before shutting down.
		if err := tracker.Flush(); err != nil {
			log.Fatalf("flush on shutdown: %v", err)
		}
	}

	// Graceful shutdown of HTTP server
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)

	log.Printf("shutdown complete")
}

func loadConfig() Config {
	var cfg Config
	flag.StringVar(&cfg.GameID, "game-id", os.Getenv("GAME_ID"), "ESPN game ID to track")
	flag.DurationVar(&cfg.PollInterval, "poll-interval", 0, "poll interval, e.g. 3s")
	flag.StringVar(&cfg.OutputPath, "output", "", "output JSON path (default shots_{gameId}.json)")
	flag.Parse()

	if cfg.GameID == "" && flag.NArg() > 0 {
		cfg.GameID = flag.Arg(0)
	}
	if cfg.GameID == "" {
		log.Fatal("missing game ID: pass --game-id, positional arg, or GAME_ID env var")
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 3 * time.Second
	}
	if cfg.OutputPath == "" {
		cfg.OutputPath = fmt.Sprintf("shots_%s.json", cfg.GameID)
	}
	return cfg
}

type Tracker struct {
	cfg         Config
	client      *http.Client
	mu          sync.Mutex
	store       ShotStore
	seen        map[string]struct{}
	nextNonce   int64
	lastStatus  string
	broadcastCh chan<- interface{}
	// cached game state for WS clients on connect
	gameStateMu sync.RWMutex
	gameState   *GameState
}

func NewTracker(cfg Config) (*Tracker, error) {
	store, nextNonce, seen, err := loadStore(cfg.OutputPath, cfg.GameID)
	if err != nil {
		return nil, err
	}
	return &Tracker{
		cfg:       cfg,
		client:    &http.Client{Timeout: 10 * time.Second},
		store:     store,
		seen:      seen,
		nextNonce: nextNonce,
	}, nil
}

// GameStateMessage returns the current game state as a WS message, or nil.
func (t *Tracker) GameStateMessage() interface{} {
	t.gameStateMu.RLock()
	gs := t.gameState
	t.gameStateMu.RUnlock()
	if gs == nil {
		return nil
	}
	return WSMessage{Type: "game_state", Data: gs}
}

func (t *Tracker) Run(ctx context.Context) error {
	ticker := time.NewTicker(t.cfg.PollInterval)
	defer ticker.Stop()

	if done, err := t.poll(ctx); err != nil {
		log.Printf("initial poll failed: %v", err)
	} else if done {
		return ErrGameFinal
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			done, err := t.poll(ctx)
			if err != nil {
				log.Printf("poll error: %v", err)
				continue
			}
			if done {
				return ErrGameFinal
			}
		}
	}
}

// poll fetches the latest data from ESPN and processes new shots.
// It returns (true, nil) when the game is final and the poller should stop.
func (t *Tracker) poll(ctx context.Context) (bool, error) {
	summary, err := fetchJSON[summaryResponse](ctx, t.client, fmt.Sprintf(summaryURLFmt, t.cfg.GameID))
	if err != nil {
		return false, fmt.Errorf("fetch summary: %w", err)
	}
	plays, err := fetchJSON[playsResponse](ctx, t.client, fmt.Sprintf(playsURLFmt, t.cfg.GameID, t.cfg.GameID))
	if err != nil {
		return false, fmt.Errorf("fetch plays: %w", err)
	}

	var gameFinal bool
	competition, ok := currentCompetition(summary)
	if ok {
		t.mu.Lock()
		if t.store.GameStartTime == "" {
			t.store.GameStartTime = competition.Date
		}
		status := competition.Status.Type.Description
		if status == "" {
			status = competition.Status.Type.Detail
		}
		if status != "" && status != t.lastStatus {
			log.Printf("game status: %s", status)
			t.lastStatus = status
		}
		t.mu.Unlock()

		// Update game state for WS clients
		gs := &GameState{
			GameID: t.cfg.GameID,
			Status: status,
		}
		if competition.Status.Type.Name != "" {
			gs.Status = competition.Status.Type.Name
		}
		for _, comp := range competition.Competitors {
			if comp.HomeAway == "home" {
				gs.HomeTeam = comp.Team.Abbreviation
				gs.HomeScore = comp.Score
			} else {
				gs.AwayTeam = comp.Team.Abbreviation
				gs.AwayScore = comp.Score
			}
		}

		// Quarter: prefer competition.Status.Period from the summary response;
		// fall back to the last play's period.number if the status field is zero.
		gs.Quarter = competition.Status.Period
		if gs.Quarter == 0 && len(plays.Items) > 0 {
			gs.Quarter = plays.Items[len(plays.Items)-1].Period.Number
		}

		// Clock from status detail
		if competition.Status.Type.Detail != "" {
			gs.Clock = competition.Status.Type.Detail
		}
		t.gameStateMu.Lock()
		t.gameState = gs
		t.gameStateMu.Unlock()

		// Broadcast updated game state
		if t.broadcastCh != nil {
			select {
			case t.broadcastCh <- WSMessage{Type: "game_state", Data: gs}:
			default:
			}
		}

		// Detect game completion via status.type.completed from the ESPN summary response.
		gameFinal = competition.Status.Type.Completed
	}

	playerMap := buildPlayerMap(&summary)
	playerNameToID := buildPlayerNameToIDMap(&summary)
	teamMap := buildTeamMap(&summary)
	playerTeamMap := buildPlayerTeamMap(&summary)

	ordered := make([]playItem, len(plays.Items))
	copy(ordered, plays.Items)
	sort.SliceStable(ordered, func(i, j int) bool {
		return sortKey(ordered[i]) < sortKey(ordered[j])
	})

	for _, play := range ordered {
		shot, ok := t.convertPlay(play, playerMap, playerNameToID, teamMap, playerTeamMap)
		if !ok {
			continue
		}
		if err := t.addShot(shot); err != nil {
			return false, err
		}
	}

	if gameFinal {
		log.Printf("[game] Game %s is final — stopping poller", t.cfg.GameID)
		if err := t.Flush(); err != nil {
			return true, fmt.Errorf("final flush: %w", err)
		}
		return true, nil
	}
	return false, nil
}

func (t *Tracker) convertPlay(play playItem, playerMap, playerNameToID, teamMap, playerTeamMap map[string]string) (ShotEvent, bool) {
	if !play.ShootingPlay || play.ID == "" {
		return ShotEvent{}, false
	}

	t.mu.Lock()
	_, exists := t.seen[play.ID]
	t.mu.Unlock()
	if exists {
		return ShotEvent{}, false
	}

	playerID, playerName := extractShooter(play, playerMap, playerNameToID)
	shotType := inferShotType(play)
	team := teamAbbreviation(play, teamMap, playerTeamMap, playerID)
	locationX, locationY := 0.0, 0.0
	if play.Coordinate != nil {
		locationX = play.Coordinate.X
		locationY = play.Coordinate.Y
	}
	zone := inferLocationZone(locationX, locationY, shotType)
	if play.Coordinate == nil {
		zone = "missing"
	} else if isInvalidCoordinate(play.Coordinate.X, play.Coordinate.Y) {
		if shotType == "free_throw" {
			zone = "free_throw"
		} else {
			zone = "invalid"
		}
	}

	return ShotEvent{
		EventID:       play.ID,
		GameID:        t.cfg.GameID,
		TimestampNS:   time.Now().UnixNano(),
		ESPNTimestamp: play.Wallclock,
		Quarter:       play.Period.Number,
		GameClock:     play.Clock.DisplayValue,
		GameClockSecs: parseGameClockSeconds(play.Clock.DisplayValue),
		PlayerID:      playerID,
		PlayerName:    playerName,
		Team:          team,
		Made:          play.ScoringPlay,
		ShotType:      shotType,
		LocationX:     locationX,
		LocationY:     locationY,
		LocationZone:  zone,
		Description:   play.Text,
		RawPayload:    marshalRawPlay(play),
	}, true
}

func (t *Tracker) addShot(shot ShotEvent) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.seen[shot.EventID]; exists {
		return nil
	}
	shot.Nonce = t.nextNonce
	t.nextNonce++
	t.store.Shots = append(t.store.Shots, shot)
	t.seen[shot.EventID] = struct{}{}
	t.store.LastUpdated = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveStore(t.cfg.OutputPath, t.store); err != nil {
		return fmt.Errorf("save store after event %s: %w", shot.EventID, err)
	}
	fmt.Printf("[Q%d %s] %s (%s) %s %s @ (%.1f, %.1f) — shots stored: %d\n",
		shot.Quarter,
		shot.GameClock,
		coalesce(shot.PlayerName, "Unknown Player"),
		coalesce(shot.Team, "?"),
		boolWord(shot.Made, "MADE", "MISSED"),
		shot.ShotTypeLabel(),
		shot.LocationX,
		shot.LocationY,
		len(t.store.Shots),
	)

	// Broadcast new shot to WS clients
	if t.broadcastCh != nil {
		select {
		case t.broadcastCh <- WSMessage{Type: "shot", Data: shot}:
		default:
			log.Printf("[ws] broadcast channel full, dropping shot %s", shot.EventID)
		}
	}

	return nil
}

func (t *Tracker) Flush() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.store.LastUpdated = time.Now().UTC().Format(time.RFC3339Nano)
	return saveStore(t.cfg.OutputPath, t.store)
}

func (s ShotEvent) ShotTypeLabel() string {
	switch s.ShotType {
	case "3pt":
		return "3PT"
	case "2pt":
		return "2PT"
	case "free_throw":
		return "FT"
	default:
		return s.ShotType
	}
}

func currentCompetition(summary summaryResponse) (summaryCompetition, bool) {
	if len(summary.Header.Competitions) == 0 {
		return summaryCompetition{}, false
	}
	return summary.Header.Competitions[0], true
}

func fetchJSON[T any](ctx context.Context, client *http.Client, url string) (T, error) {
	var zero T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return zero, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return zero, fmt.Errorf("http %d from %s: %s", resp.StatusCode, url, string(body))
	}
	decoder := json.NewDecoder(resp.Body)
	var out T
	if err := decoder.Decode(&out); err != nil {
		return zero, err
	}
	return out, nil
}

func loadStore(path, gameID string) (ShotStore, int64, map[string]struct{}, error) {
	store := ShotStore{
		GameID: gameID,
		LocationSchema: LocationSchema{
			Description: "ESPN play-by-play API v2",
			XRange:      [2]float64{0, 50},
			YRange:      [2]float64{0, 30},
			Origin:      "bottom-left corner of half-court (baseline)",
			Units:       "normalized (not feet — appears to be a 50x30ish grid for half-court)",
			Notes:       "X=0 is left sideline, X=49 is right sideline, Y=0 is baseline (under basket), Y~30 is halfcourt line. Free throws have sentinel coords (-214748340) — stored with location_x/y = 0 and location_zone = 'free_throw'. Coordinate 25,0 is roughly center of basket.",
		},
		Shots: []ShotEvent{},
	}
	seen := make(map[string]struct{})
	nextNonce := int64(1)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store, nextNonce, seen, nil
		}
		return ShotStore{}, 0, nil, err
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return ShotStore{}, 0, nil, err
	}
	if store.GameID == "" {
		store.GameID = gameID
	}
	for _, shot := range store.Shots {
		seen[shot.EventID] = struct{}{}
		if shot.Nonce >= nextNonce {
			nextNonce = shot.Nonce + 1
		}
	}
	return store, nextNonce, seen, nil
}

func saveStore(path string, store ShotStore) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func boolWord(v bool, yes, no string) string {
	if v {
		return yes
	}
	return no
}

func sortKey(play playItem) int {
	if play.SequenceNumber != "" {
		if n, err := strconv.Atoi(play.SequenceNumber); err == nil {
			return n
		}
	}
	if n, err := strconv.Atoi(play.ID); err == nil {
		return n
	}
	return 0
}

func coalesce(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
