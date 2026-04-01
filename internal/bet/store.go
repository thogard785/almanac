package bet

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/almanac/espn-shots/internal/persist"
)

// Store handles persistence for bets and results.
type Store struct {
	dir        string
	mu         sync.RWMutex
	bets       []*Bet
	results    []*BetResult
	persistCh  chan func()
}

// NewStore creates a bet store in the given directory.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		dir:       dir,
		persistCh: make(chan func(), 100),
	}
	if err := s.loadExisting(); err != nil {
		return nil, fmt.Errorf("load existing bets: %w", err)
	}
	return s, nil
}

// RunPersistLoop processes persistence operations serially.
func (s *Store) RunPersistLoop(done <-chan struct{}) {
	for {
		select {
		case <-done:
			// Drain remaining
			for {
				select {
				case fn := <-s.persistCh:
					fn()
				default:
					return
				}
			}
		case fn := <-s.persistCh:
			fn()
		}
	}
}

// SaveBet persists a bet to disk.
func (s *Store) SaveBet(b *Bet) {
	s.mu.Lock()
	s.bets = append(s.bets, b)
	s.mu.Unlock()

	s.persistCh <- func() {
		s.mu.RLock()
		bets := make([]*Bet, len(s.bets))
		copy(bets, s.bets)
		s.mu.RUnlock()

		filename := fmt.Sprintf("bets_%s.json", today())
		if err := persist.SaveFile(filepath.Join(s.dir, filename), bets); err != nil {
			log.Printf("[bet-store] save bets error: %v", err)
		}
	}
}

// SaveResult persists a bet result to disk.
func (s *Store) SaveResult(r *BetResult) {
	s.mu.Lock()
	s.results = append(s.results, r)
	s.mu.Unlock()

	s.persistCh <- func() {
		s.mu.RLock()
		results := make([]*BetResult, len(s.results))
		copy(results, s.results)
		s.mu.RUnlock()

		filename := fmt.Sprintf("results_%s.json", today())
		if err := persist.SaveFile(filepath.Join(s.dir, filename), results); err != nil {
			log.Printf("[bet-store] save results error: %v", err)
		}
	}
}

// BetsByWallet returns all bets for a wallet address.
func (s *Store) BetsByWallet(wallet string) []*Bet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Bet
	for _, b := range s.bets {
		if b.WalletAddr == wallet {
			result = append(result, b)
		}
	}
	return result
}

// ResultsByGame returns all results for a game.
func (s *Store) ResultsByGame(gameID string) []*BetResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*BetResult
	for _, r := range s.results {
		if r.GameID == gameID {
			result = append(result, r)
		}
	}
	return result
}

// AllBets returns all stored bets.
func (s *Store) AllBets() []*Bet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bets := make([]*Bet, len(s.bets))
	copy(bets, s.bets)
	return bets
}

func (s *Store) loadExisting() error {
	pattern := filepath.Join(s.dir, "bets_*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, f := range files {
		var bets []*Bet
		if err := persist.LoadFile(f, &bets); err != nil {
			log.Printf("[bet-store] load %s error: %v", f, err)
			continue
		}
		s.bets = append(s.bets, bets...)
	}

	pattern = filepath.Join(s.dir, "results_*.json")
	files, err = filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, f := range files {
		var results []*BetResult
		if err := persist.LoadFile(f, &results); err != nil {
			log.Printf("[bet-store] load %s error: %v", f, err)
			continue
		}
		s.results = append(s.results, results...)
	}

	log.Printf("[bet-store] loaded %d bets, %d results", len(s.bets), len(s.results))
	return nil
}

func today() string {
	return time.Now().UTC().Format("2006-01-02")
}
