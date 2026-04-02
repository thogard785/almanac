package sim

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/almanac/espn-shots/internal/game"
	"github.com/almanac/espn-shots/internal/persist"
)

// SavedGame is the on-disk record of a completed game available for replay.
type SavedGame struct {
	GameID  string           `json:"game_id"`
	Sport   string           `json:"sport"`
	State   game.GameState   `json:"state"`
	Plays   []game.PlayEvent `json:"plays"`
	SavedAt time.Time        `json:"saved_at"`
}

// SaveCompletedGame persists a completed game's plays so it can be replayed.
func SaveCompletedGame(dir string, gameID string, sport game.Sport, state game.GameState, plays []game.PlayEvent) error {
	if len(plays) == 0 {
		return fmt.Errorf("no plays to save")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sg := SavedGame{
		GameID:  gameID,
		Sport:   string(sport),
		State:   state,
		Plays:   plays,
		SavedAt: time.Now().UTC(),
	}
	filename := fmt.Sprintf("game_%s_%s.json", sport, gameID)
	return persist.SaveFile(filepath.Join(dir, filename), sg)
}

// LoadLatestGame loads the most recently saved completed game from disk.
func LoadLatestGame(dir string) (*SavedGame, error) {
	pattern := filepath.Join(dir, "game_*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no saved games found in %s", dir)
	}

	// Sort by modification time, newest first.
	sort.Slice(files, func(i, j int) bool {
		fi, _ := os.Stat(files[i])
		fj, _ := os.Stat(files[j])
		if fi == nil || fj == nil {
			return false
		}
		return fi.ModTime().After(fj.ModTime())
	})

	for _, file := range files {
		var sg SavedGame
		if err := persist.LoadFile(file, &sg); err != nil {
			log.Printf("[sim] failed to load %s: %v", file, err)
			continue
		}
		if len(sg.Plays) == 0 {
			continue
		}
		return &sg, nil
	}
	return nil, fmt.Errorf("no valid saved games found in %s", dir)
}
