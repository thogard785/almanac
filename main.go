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
	"sync"
	"syscall"
	"time"
)

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

	log.Printf("tracking game=%s poll=%s output=%s", cfg.GameID, cfg.PollInterval, cfg.OutputPath)
	if err := tracker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("run tracker: %v", err)
	}
	if err := tracker.Flush(); err != nil {
		log.Fatalf("flush on shutdown: %v", err)
	}
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
	cfg        Config
	client     *http.Client
	mu         sync.Mutex
	store      ShotStore
	seen       map[string]struct{}
	nextNonce  int64
	lastStatus string
}

func NewTracker(cfg Config) (*Tracker, error) {
	store, nextNonce, seen, err := loadStore(cfg.OutputPath, cfg.GameID)
	if err != nil {
		return nil, err
	}
	return &Tracker{
		cfg:        cfg,
		client:     &http.Client{Timeout: 10 * time.Second},
		store:      store,
		seen:       seen,
		nextNonce:  nextNonce,
		lastStatus: "",
	}, nil
}

func (t *Tracker) Run(ctx context.Context) error {
	ticker := time.NewTicker(t.cfg.PollInterval)
	defer ticker.Stop()

	if err := t.poll(ctx); err != nil {
		log.Printf("initial poll failed: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := t.poll(ctx); err != nil {
				log.Printf("poll error: %v", err)
			}
		}
	}
}

func (t *Tracker) poll(ctx context.Context) error {
	summary, err := fetchJSON[summaryResponse](ctx, t.client, fmt.Sprintf(summaryURLFmt, t.cfg.GameID))
	if err != nil {
		return fmt.Errorf("fetch summary: %w", err)
	}
	plays, err := fetchJSON[playsResponse](ctx, t.client, fmt.Sprintf(playsURLFmt, t.cfg.GameID, t.cfg.GameID))
	if err != nil {
		return fmt.Errorf("fetch plays: %w", err)
	}

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
	}

	playerMap := buildPlayerMap(&summary)
	teamMap := buildTeamMap(&summary)

	ordered := make([]playItem, len(plays.Items))
	copy(ordered, plays.Items)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].ID < ordered[j].ID
	})

	for _, play := range ordered {
		shot, ok := t.convertPlay(play, playerMap, teamMap)
		if !ok {
			continue
		}
		if err := t.addShot(shot); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tracker) convertPlay(play playItem, playerMap, teamMap map[string]string) (ShotEvent, bool) {
	if !play.ShootingPlay || play.ID == "" {
		return ShotEvent{}, false
	}

	t.mu.Lock()
	_, exists := t.seen[play.ID]
	t.mu.Unlock()
	if exists {
		return ShotEvent{}, false
	}

	playerID, playerName := extractShooter(play, playerMap)
	shotType := inferShotType(play)
	team := teamAbbreviation(play, teamMap)
	locationX, locationY := 0.0, 0.0
	if play.Coordinate != nil {
		locationX = play.Coordinate.X
		locationY = play.Coordinate.Y
	}
	zone := inferLocationZone(locationX, locationY, shotType)
	if play.Coordinate == nil {
		zone = "missing"
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
	fmt.Printf("[Q%d %s] %s %s %s @ (%.1f, %.1f) | shots: %d\n",
		shot.Quarter,
		shot.GameClock,
		coalesce(shot.PlayerName, "Unknown Player"),
		boolWord(shot.Made, "MADE", "MISS"),
		shot.ShotTypeLabel(),
		shot.LocationX,
		shot.LocationY,
		len(t.store.Shots),
	)
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

func coalesce(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
