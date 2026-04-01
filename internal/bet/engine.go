package bet

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/almanac/espn-shots/internal/game"
	"github.com/google/uuid"
)

type Engine struct {
	store *Store

	mu          sync.Mutex
	pending     map[string][]*Bet
	processed   map[string]game.PlayEvent
	seenNonce   map[[20]byte]map[uint64]struct{}
	resultChan  chan WalletResult
	balanceChan chan BalanceUpdate
}

type WalletResult struct {
	Wallet [20]byte
	Result *BetResult
}

func NewEngine(store *Store) *Engine {
	return &Engine{
		store:       store,
		pending:     make(map[string][]*Bet),
		processed:   make(map[string]game.PlayEvent),
		seenNonce:   make(map[[20]byte]map[uint64]struct{}),
		resultChan:  make(chan WalletResult, 512),
		balanceChan: make(chan BalanceUpdate, 512),
	}
}

func (e *Engine) Run(ctx context.Context) {
	<-ctx.Done()
}

func (e *Engine) PlaceBet(b *Bet) error {
	if b == nil {
		return fmt.Errorf("missing bet")
	}
	if b.GameID == "" || b.PlayID == "" {
		return fmt.Errorf("missing game_id or play_id")
	}
	if b.Amount <= 0 {
		return fmt.Errorf("amount must be greater than zero")
	}
	if err := VerifySignature(b); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.seenNonce[b.Wallet] == nil {
		e.seenNonce[b.Wallet] = make(map[uint64]struct{})
	}
	if _, exists := e.seenNonce[b.Wallet][b.Nonce]; exists {
		return fmt.Errorf("duplicate nonce")
	}
	e.seenNonce[b.Wallet][b.Nonce] = struct{}{}

	b.BetID = uuid.NewString()
	b.ReceivedAt = time.Now().UTC()
	b.Status = "pending"
	e.pending[b.PlayID] = append(e.pending[b.PlayID], b)
	e.store.SaveBet(b)

	if event, ok := e.processed[b.PlayID]; ok {
		go e.resolveForEvent(event)
	}
	return nil
}

func (e *Engine) OnPlayEvent(event game.PlayEvent) {
	e.mu.Lock()
	e.processed[event.PlayID] = event
	e.mu.Unlock()
	go e.resolveForEvent(event)
}

func (e *Engine) ResultChan() <-chan WalletResult   { return e.resultChan }
func (e *Engine) BalanceChan() <-chan BalanceUpdate { return e.balanceChan }

func (e *Engine) resolveForEvent(event game.PlayEvent) {
	e.mu.Lock()
	bets := append([]*Bet(nil), e.pending[event.PlayID]...)
	if len(bets) == 0 {
		e.mu.Unlock()
		return
	}
	delete(e.pending, event.PlayID)
	e.mu.Unlock()

	for _, b := range bets {
		result := e.resolveBet(b, event)
		e.store.SaveResult(result)
		select {
		case e.resultChan <- WalletResult{Wallet: b.Wallet, Result: result}:
		default:
			log.Printf("[bet-engine] result channel full for bet %s", b.BetID)
		}
		select {
		case e.balanceChan <- BalanceUpdate{Type: "balance_update", Wallet: WalletHex(b.Wallet), Balance: getUserBalance(b.Wallet)}:
		default:
			log.Printf("[bet-engine] balance channel full for bet %s", b.BetID)
		}
	}
}

func (e *Engine) resolveBet(b *Bet, event game.PlayEvent) *BetResult {
	result := &BetResult{
		Type:       "bet_result",
		BetID:      b.BetID,
		PlayID:     b.PlayID,
		GameID:     b.GameID,
		Coordinate: b.Coordinate,
		Payout:     0,
		Result:     "lost",
	}

	if event.Timestamp.IsZero() {
		b.Status = "invalid"
		b.InvalidReason = "play timestamp unavailable"
		result.Result = "invalid"
		result.Reason = b.InvalidReason
		return result
	}

	if !b.ReceivedAt.Before(event.Timestamp.Add(-1 * time.Second)) {
		b.Status = "invalid"
		b.InvalidReason = "received too late"
		result.Result = "invalid"
		result.Reason = b.InvalidReason
		return result
	}

	if event.Location == nil {
		b.Status = "lost"
		result.Result = "lost"
		result.Reason = "play has no location data"
		return result
	}

	result.PlayLocation = &game.Coord{X: event.Location.X, Y: event.Location.Y}
	dx := b.Coordinate.X - event.Location.X
	dy := b.Coordinate.Y - event.Location.Y
	result.Distance = math.Sqrt(dx*dx + dy*dy)
	if result.Distance <= WinRadius {
		b.Status = "won"
		result.Result = "won"
		result.Payout = math.Trunc((b.Amount*2)*100) / 100
		return result
	}

	b.Status = "lost"
	return result
}
