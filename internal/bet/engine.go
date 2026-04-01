package bet

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/almanac/espn-shots/internal/game"
)

// WinRadii holds win radius settings per sport.
type WinRadii struct {
	NBA float64
	MLB float64
	PGA float64
}

// Engine accepts bets, stores them per PlayID, and resolves retroactively
// when play events arrive.
type Engine struct {
	store     *Store
	radii     WinRadii
	minLeadMs int64

	betChan    chan *Bet
	resultChan chan *BetResult

	// Pending bets indexed by PlayID
	pendingMu sync.RWMutex
	pending   map[string][]*Bet

	// Resolved play IDs to avoid double resolution
	resolvedMu sync.RWMutex
	resolved   map[string]struct{}
}

// NewEngine creates a new bet engine.
func NewEngine(store *Store, radii WinRadii, minLeadMs int64) *Engine {
	return &Engine{
		store:      store,
		radii:      radii,
		minLeadMs:  minLeadMs,
		betChan:    make(chan *Bet, 1000),
		resultChan: make(chan *BetResult, 1000),
		pending:    make(map[string][]*Bet),
		resolved:   make(map[string]struct{}),
	}
}

// BetChan returns the channel for submitting bets.
func (e *Engine) BetChan() chan<- *Bet { return e.betChan }

// ResultChan returns the channel for reading results.
func (e *Engine) ResultChan() <-chan *BetResult { return e.resultChan }

// Run starts the bet processing loop. Blocks until ctx is done.
func (e *Engine) Run(ctx context.Context) {
	log.Printf("[bet-engine] starting")
	for {
		select {
		case <-ctx.Done():
			log.Printf("[bet-engine] stopping")
			return
		case b, ok := <-e.betChan:
			if !ok {
				return
			}
			e.processBet(b)
		}
	}
}

// PlaceBet validates and queues a bet.
func (e *Engine) PlaceBet(b *Bet) error {
	if b.Sport == "" || b.GameID == "" || b.PlayID == "" || b.WalletAddr == "" {
		return fmt.Errorf("missing required fields")
	}

	// Validate signature and normalize wallet address.
	recovered, err := ValidateSignature(b)
	if err != nil {
		return fmt.Errorf("signature validation: %w", err)
	}
	b.WalletAddr = recovered

	if err := e.verifyBalance(b); err != nil {
		return fmt.Errorf("balance verification: %w", err)
	}

	// Assign ID and timestamp
	b.ID = generateID()
	b.ReceivedAt = time.Now().UnixNano()

	select {
	case e.betChan <- b:
		return nil
	default:
		return fmt.Errorf("bet queue full")
	}
}

// OnPlayEvent is called when a new play event arrives. Resolves pending bets.
func (e *Engine) OnPlayEvent(event game.SportEvent) {
	playID := event.GetEventID()
	gameID := event.GetGameID()
	sport := event.GetSport()

	e.resolvedMu.RLock()
	_, alreadyResolved := e.resolved[playID]
	e.resolvedMu.RUnlock()
	if alreadyResolved {
		return
	}

	e.pendingMu.RLock()
	bets := e.pending[playID]
	e.pendingMu.RUnlock()

	if len(bets) == 0 {
		return
	}

	// Resolve in a goroutine to not block the poll loop
	go func() {
		e.resolvedMu.Lock()
		e.resolved[playID] = struct{}{}
		e.resolvedMu.Unlock()

		playTimestamp := event.GetTimestamp()
		playTimeNS := game.TimestampToUnixNano(playTimestamp)

		radius := e.radiusForSport(sport)

		for _, b := range bets {
			result := e.resolveBet(b, event, playTimestamp, playTimeNS, radius, gameID)
			e.store.SaveResult(result)
			select {
			case e.resultChan <- result:
			default:
				log.Printf("[bet-engine] result channel full for bet %s", b.ID)
			}
		}

		// Remove from pending
		e.pendingMu.Lock()
		delete(e.pending, playID)
		e.pendingMu.Unlock()

		log.Printf("[bet-engine] resolved %d bets for play %s", len(bets), playID)
	}()
}

func (e *Engine) processBet(b *Bet) {
	e.store.SaveBet(b)

	e.pendingMu.Lock()
	e.pending[b.PlayID] = append(e.pending[b.PlayID], b)
	e.pendingMu.Unlock()

	log.Printf("[bet-engine] accepted bet %s from %s on %s play %s", b.ID, b.WalletAddr, b.Sport, b.PlayID)
}

func (e *Engine) resolveBet(b *Bet, event game.SportEvent, playTimestamp string, playTimeNS int64, radius float64, gameID string) *BetResult {
	result := &BetResult{
		BetID:         b.ID,
		PlayID:        b.PlayID,
		Sport:         b.Sport,
		GameID:        gameID,
		WalletAddr:    b.WalletAddr,
		PredictedX:    b.LocationX,
		PredictedY:    b.LocationY,
		PredictedZone: b.Zone,
		ActualX:       event.GetLocationX(),
		ActualY:       event.GetLocationY(),
		ActualZone:    event.GetZone(),
		WinRadius:     radius,
		BetReceivedAt: b.ReceivedAt,
		PlayTimestamp: playTimestamp,
		ProcessedAt:   time.Now().UnixNano(),
	}

	// Check timing: bet must be received > minLeadMs before play
	if playTimeNS > 0 {
		result.TimeDeltaMs = (playTimeNS - b.ReceivedAt) / int64(time.Millisecond)
		result.ValidBet = result.TimeDeltaMs >= e.minLeadMs
	} else {
		// No play timestamp — conservatively mark as valid
		result.ValidBet = true
		result.TimeDeltaMs = 0
	}

	if !result.ValidBet {
		result.Won = false
		return result
	}

	// Check win condition
	if b.Zone != "" && event.GetZone() != "" {
		// Zone-based matching
		result.Won = strings.EqualFold(b.Zone, event.GetZone())
	} else if event.HasCoordinates() {
		// Coordinate-based matching
		dx := b.LocationX - event.GetLocationX()
		dy := b.LocationY - event.GetLocationY()
		result.Distance = math.Sqrt(dx*dx + dy*dy)
		result.Won = result.Distance <= radius
	}

	if err := e.processPayout(result); err != nil {
		log.Printf("[bet-engine] payout stub failed for bet %s: %v", b.ID, err)
	}

	return result
}

func (e *Engine) verifyBalance(b *Bet) error {
	if b == nil {
		return fmt.Errorf("nil bet")
	}
	// Stub for future on-chain balance verification.
	// Keep the flow non-blocking for backend integration/testing.
	return nil
}

func (e *Engine) processPayout(result *BetResult) error {
	if result == nil || !result.Won {
		return nil
	}
	// Stub for future on-chain settlement.
	log.Printf("[bet-engine] payout TODO for winning bet %s wallet=%s game=%s", result.BetID, result.WalletAddr, result.GameID)
	return nil
}

func (e *Engine) radiusForSport(sport game.Sport) float64 {
	switch sport {
	case game.SportNBA:
		return e.radii.NBA
	case game.SportMLB:
		return e.radii.MLB
	case game.SportPGA:
		return e.radii.PGA
	default:
		return 5.0
	}
}

func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}
