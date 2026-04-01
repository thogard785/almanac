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

type Store struct {
	dir       string
	mu        sync.RWMutex
	bets      []*Bet
	results   []*BetResult
	persistCh chan func()
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		dir:       dir,
		persistCh: make(chan func(), 128),
	}
	if err := s.loadExisting(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) RunPersistLoop(done <-chan struct{}) {
	for {
		select {
		case <-done:
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

func (s *Store) SaveBet(b *Bet) {
	s.mu.Lock()
	s.bets = append(s.bets, b)
	s.mu.Unlock()
	s.enqueuePersist("bets", func() any {
		s.mu.RLock()
		defer s.mu.RUnlock()
		out := make([]*Bet, len(s.bets))
		copy(out, s.bets)
		return out
	})
}

func (s *Store) SaveResult(r *BetResult) {
	s.mu.Lock()
	s.results = append(s.results, r)
	s.mu.Unlock()
	s.enqueuePersist("results", func() any {
		s.mu.RLock()
		defer s.mu.RUnlock()
		out := make([]*BetResult, len(s.results))
		copy(out, s.results)
		return out
	})
}

func (s *Store) BetsByWallet(wallet [20]byte) []*Bet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Bet
	for _, b := range s.bets {
		if b.Wallet == wallet {
			out = append(out, b)
		}
	}
	return out
}

func (s *Store) ResultsByWallet(wallet [20]byte) []*BetResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hex := WalletHex(wallet)
	var out []*BetResult
	for _, r := range s.results {
		if r != nil && r.Type == "bet_result" && hex != "" {
			// wallet is implicit in BetID lookup on the backend; results are routed live.
			// Historical replay is intentionally omitted for now to keep the store simple.
		}
	}
	return out
}

func (s *Store) AllBets() []*Bet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Bet, len(s.bets))
	copy(out, s.bets)
	return out
}

func (s *Store) enqueuePersist(prefix string, snapshot func() any) {
	select {
	case s.persistCh <- func() {
		filename := fmt.Sprintf("%s_%s.json", prefix, time.Now().UTC().Format("2006-01-02"))
		if err := persist.SaveFile(filepath.Join(s.dir, filename), snapshot()); err != nil {
			log.Printf("[bet-store] persist %s failed: %v", prefix, err)
		}
	}:
	default:
		log.Printf("[bet-store] persist queue full for %s", prefix)
	}
}

func (s *Store) loadExisting() error {
	for _, pattern := range []string{"bets_*.json", "results_*.json"} {
		files, err := filepath.Glob(filepath.Join(s.dir, pattern))
		if err != nil {
			return err
		}
		for _, file := range files {
			switch {
			case pattern[:4] == "bets":
				var bets []*Bet
				if err := persist.LoadFile(file, &bets); err != nil {
					log.Printf("[bet-store] load %s failed: %v", file, err)
					continue
				}
				s.bets = append(s.bets, bets...)
			default:
				var results []*BetResult
				if err := persist.LoadFile(file, &results); err != nil {
					log.Printf("[bet-store] load %s failed: %v", file, err)
					continue
				}
				s.results = append(s.results, results...)
			}
		}
	}
	return nil
}
