package bet

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/almanac/espn-shots/internal/game"
	"github.com/google/uuid"
)

type Engine struct {
	store    *Store
	balances BalanceProvider

	nextRoundResolution bool

	mu                 sync.Mutex
	pending            map[string][]*Bet
	processed          map[string]game.PlayEvent
	seenNonce          map[[20]byte]map[uint64]struct{}
	knownRounds        map[string]game.PlayEvent
	currentRoundByGame map[string]string
	resultChan         chan WalletResult
	balanceChan        chan BalanceUpdate
}

type WalletResult struct {
	Wallet [20]byte
	Result *BetResult
}

type BalanceUpdate struct {
	Type       string  `json:"type"`
	Wallet     string  `json:"wallet"`
	Balance    float64 `json:"balance"`
	Simulation bool    `json:"simulation"`
}

func NewEngine(store *Store) *Engine {
	return NewEngineWithBalance(store, DefaultBalanceProvider)
}

func NewEngineWithBalance(store *Store, bp BalanceProvider) *Engine {
	e := &Engine{
		store:              store,
		balances:           bp,
		pending:            make(map[string][]*Bet),
		processed:          make(map[string]game.PlayEvent),
		seenNonce:          make(map[[20]byte]map[uint64]struct{}),
		knownRounds:        make(map[string]game.PlayEvent),
		currentRoundByGame: make(map[string]string),
		resultChan:         make(chan WalletResult, 512),
		balanceChan:        make(chan BalanceUpdate, 512),
	}
	for _, b := range store.AllBets() {
		if b == nil {
			continue
		}
		if e.seenNonce[b.Wallet] == nil {
			e.seenNonce[b.Wallet] = make(map[uint64]struct{})
		}
		e.seenNonce[b.Wallet][b.Nonce] = struct{}{}
	}
	return e
}

func (e *Engine) Run(ctx context.Context) {
	<-ctx.Done()
}

func (e *Engine) EnableNextRoundResolution() *Engine {
	e.nextRoundResolution = true
	return e
}

func (e *Engine) HasSeenNonce(wallet [20]byte, nonce uint64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.seenNonce[wallet][nonce]
	return ok
}

func (e *Engine) CurrentRound(gameID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.currentRoundByGame[gameID]
}

func (e *Engine) PlaceBet(b *Bet) (*BetAck, error) {
	if b == nil {
		return nil, fmt.Errorf("missing bet")
	}
	if b.GameID == "" || b.RoundID == "" {
		return e.rejectBet(b, "missing gameId or roundId"), nil
	}
	if b.Amount < MinimumBetAmount {
		return e.rejectBet(b, "amount below minimum bet amount"), nil
	}
	if err := VerifySignature(b); err != nil {
		return e.rejectBet(b, err.Error()), nil
	}
	if math.Abs(float64(time.Now().Unix()-b.Timestamp)) > float64(BetExpiryWindow) {
		return e.rejectBet(b, "stale bet timestamp"), nil
	}
	if b.MinimumMultiplier > ActualMultiplier {
		return e.rejectBet(b, "minimum multiplier too high"), nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.seenNonce[b.Wallet] == nil {
		e.seenNonce[b.Wallet] = make(map[uint64]struct{})
	}
	if _, exists := e.seenNonce[b.Wallet][b.Nonce]; exists {
		return e.rejectBetLocked(b, "duplicate nonce"), nil
	}
	currentRound := e.currentRoundByGame[b.GameID]
	if currentRound == "" || currentRound != b.RoundID {
		return e.rejectBetLocked(b, "unknown or resolved roundId"), nil
	}
	if _, ok := e.knownRounds[b.RoundID]; !ok {
		return e.rejectBetLocked(b, "unknown or resolved roundId"), nil
	}
	if e.balances.GetBalance(b.Wallet) < b.Amount {
		return e.rejectBetLocked(b, "insufficient balance"), nil
	}

	e.seenNonce[b.Wallet][b.Nonce] = struct{}{}
	b.BetID = uuid.NewString()
	b.ReceivedAt = time.Now().UTC()
	b.Status = "live"
	b.ActualMultiplier = ActualMultiplier
	b.Payout = 0
	e.pending[b.RoundID] = append(e.pending[b.RoundID], b)
	e.store.SaveBet(b)
	balance := e.balances.AddBalance(b.Wallet, -b.Amount)
	ack := &BetAck{Type: "bet_ack", Status: "accepted", GameID: b.GameID, Nonce: b.Nonce, Timestamp: time.Now().Unix(), Balance: balance, ActualMultiplier: ActualMultiplier, Simulation: b.Simulation, ContractBinding: b.ContractBinding, ContractResolution: b.ContractResolution}
	select {
	case e.balanceChan <- BalanceUpdate{Type: "balance_update", Wallet: WalletHex(b.Wallet), Balance: balance, Simulation: b.Simulation}:
	default:
	}
	if !e.nextRoundResolution {
		if event, ok := e.processed[b.RoundID]; ok {
			go e.resolveForEvent(event)
		}
	}
	return ack, nil
}

func (e *Engine) RejectBet(b *Bet, reason string, resolution *game.BetContractResolution) *BetAck {
	if b != nil && resolution != nil {
		b.ContractResolution = resolution
	}
	return e.rejectBet(b, reason)
}

func (e *Engine) rejectBet(b *Bet, reason string) *BetAck {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rejectBetLocked(b, reason)
}

func (e *Engine) rejectBetLocked(b *Bet, reason string) *BetAck {
	if b == nil {
		return &BetAck{Type: "bet_ack", Status: "rejected", Timestamp: time.Now().Unix(), RejectionReason: reason, ActualMultiplier: ActualMultiplier}
	}
	b.BetID = uuid.NewString()
	b.ReceivedAt = time.Now().UTC()
	b.Status = "invalid"
	b.ActualMultiplier = ActualMultiplier
	b.RejectionReason = reason
	if b.ContractBinding != nil && b.ContractResolution == nil {
		b.ContractResolution = &game.BetContractResolution{
			ContractVersion: game.ContractVersionAssumedPossessionV1,
			Kind:            game.ResolutionRejectedInvalid,
			Reasoning:       reason,
		}
	}
	e.store.SaveBet(b)
	return &BetAck{
		Type:               "bet_ack",
		Status:             "rejected",
		GameID:             b.GameID,
		Nonce:              b.Nonce,
		Timestamp:          time.Now().Unix(),
		Balance:            e.balances.GetBalance(b.Wallet),
		ActualMultiplier:   ActualMultiplier,
		RejectionReason:    reason,
		Simulation:         b.Simulation,
		ContractBinding:    b.ContractBinding,
		ContractResolution: b.ContractResolution,
	}
}

func (e *Engine) OnPlayEvent(event game.PlayEvent) {
	e.mu.Lock()
	e.processed[event.PlayID] = event
	if e.nextRoundResolution {
		prevRound := e.currentRoundByGame[event.GameID]
		e.knownRounds[event.PlayID] = event
		e.currentRoundByGame[event.GameID] = event.PlayID
		if prevRound != "" {
			delete(e.knownRounds, prevRound)
		}
		e.mu.Unlock()
		if prevRound != "" {
			go e.resolveRound(prevRound, event)
		}
		return
	}
	e.knownRounds[event.PlayID] = event
	e.currentRoundByGame[event.GameID] = event.PlayID
	e.mu.Unlock()
	go e.resolveForEvent(event)
}

func (e *Engine) ResultChan() <-chan WalletResult   { return e.resultChan }
func (e *Engine) BalanceChan() <-chan BalanceUpdate { return e.balanceChan }

func (e *Engine) resolveForEvent(event game.PlayEvent) {
	e.resolveRound(event.PlayID, event)
}

func (e *Engine) resolveRound(roundID string, event game.PlayEvent) {
	e.mu.Lock()
	bets := append([]*Bet(nil), e.pending[roundID]...)
	if len(bets) == 0 {
		e.mu.Unlock()
		return
	}
	delete(e.pending, roundID)
	if !e.nextRoundResolution {
		delete(e.knownRounds, roundID)
	}
	e.mu.Unlock()

	for _, b := range bets {
		result := e.resolveBet(b, event)
		e.store.SaveBet(b)
		e.store.SaveResult(result)
		if result != nil {
			resolutionKind := ""
			if result.ContractResolution != nil {
				resolutionKind = string(result.ContractResolution.Kind)
			}
			log.Printf("[bet-engine] resolved game=%s bound_round=%s event=%s wallet=%s outcome=%s resolution=%s assumed_team=%s actual_team=%s simulation=%t", b.GameID, b.RoundID, event.PlayID, WalletHex(b.Wallet), result.Outcome, resolutionKind, contractAssumedTeam(b), game.ActualShotTeam(event), b.Simulation)
		}
		select {
		case e.resultChan <- WalletResult{Wallet: b.Wallet, Result: result}:
		default:
			log.Printf("[bet-engine] result channel full for bet %s", b.BetID)
		}
		select {
		case e.balanceChan <- BalanceUpdate{Type: "balance_update", Wallet: WalletHex(b.Wallet), Balance: result.Balance, Simulation: b.Simulation}:
		default:
			log.Printf("[bet-engine] balance channel full for bet %s", b.BetID)
		}
	}
}

func (e *Engine) resolveBet(b *Bet, event game.PlayEvent) *BetResult {
	result := &BetResult{
		Type:             "bet_result",
		Outcome:          "loss",
		Wallet:           WalletHex(b.Wallet),
		Nonce:            b.Nonce,
		GameID:           b.GameID,
		RoundID:          b.RoundID,
		BetCoordinates:   b.Coordinate,
		BetRadius:        b.BetRadius,
		BackendTimestamp: time.Now().Unix(),
		EventTimestamp:   event.Timestamp.Unix(),
		AmountBet:        b.Amount,
		AmountWon:        0,
		Simulation:       b.Simulation,
		Balance:          e.balances.GetBalance(b.Wallet),
		IsHistorical:     false,
		ContractBinding:  b.ContractBinding,
	}

	actualShotTeam := game.ActualShotTeam(event)
	if b.ContractBinding != nil {
		if resolution, applied := e.resolveContractBet(b, event, actualShotTeam, result); applied {
			b.ContractResolution = resolution
			result.ContractResolution = resolution
			return result
		}
	}

	if event.Timestamp.IsZero() {
		b.Status = "nullified"
		b.NullificationReason = "play timestamp unavailable"
		result.Outcome = "nullified"
		result.NullificationReason = b.NullificationReason
		result.Balance = e.balances.AddBalance(b.Wallet, b.Amount)
		return result
	}

	if !b.ReceivedAt.Before(event.Timestamp.Add(-1 * time.Second)) {
		b.Status = "nullified"
		b.NullificationReason = "received too late"
		result.Outcome = "nullified"
		result.NullificationReason = b.NullificationReason
		result.Balance = e.balances.AddBalance(b.Wallet, b.Amount)
		return result
	}

	if event.Location == nil {
		b.Status = "nullified"
		b.NullificationReason = "play has no location data"
		result.Outcome = "nullified"
		result.NullificationReason = b.NullificationReason
		result.Balance = e.balances.AddBalance(b.Wallet, b.Amount)
		return result
	}

	dx := b.Coordinate.X - event.Location.X
	dy := b.Coordinate.Y - event.Location.Y
	distance := math.Sqrt(dx*dx + dy*dy)
	if distance <= b.BetRadius {
		b.Status = "win"
		b.Payout = math.Round((b.Amount*2)*100) / 100
		result.Outcome = "win"
		result.AmountWon = b.Payout
		result.Balance = e.balances.AddBalance(b.Wallet, b.Payout)
		return result
	}

	b.Status = "loss"
	result.Outcome = "loss"
	result.Balance = e.balances.GetBalance(b.Wallet)
	return result
}

func contractAssumedTeam(b *Bet) string {
	if b == nil || b.ContractBinding == nil {
		return ""
	}
	return b.ContractBinding.AssumedTeam
}

func (e *Engine) resolveContractBet(b *Bet, event game.PlayEvent, actualShotTeam string, result *BetResult) (*game.BetContractResolution, bool) {
	if b == nil || b.ContractBinding == nil || result == nil {
		return nil, false
	}

	if event.Timestamp.IsZero() {
		b.Status = "nullified"
		b.NullificationReason = "play timestamp unavailable"
		result.Outcome = "nullified"
		result.NullificationReason = b.NullificationReason
		result.Balance = e.balances.AddBalance(b.Wallet, b.Amount)
		return &game.BetContractResolution{
			ContractVersion:     game.ContractVersionAssumedPossessionV1,
			Kind:                game.ResolutionNullifiedWrongTeam,
			NullificationReason: game.NullificationReasonMissingEventTimestamp,
			Reasoning:           "accepted bet refunded because the resolving event timestamp was unavailable",
		}, true
	}

	if actualShotTeam == "" {
		b.Status = "nullified"
		b.NullificationReason = "play missing team data"
		result.Outcome = "nullified"
		result.NullificationReason = b.NullificationReason
		result.Balance = e.balances.AddBalance(b.Wallet, b.Amount)
		return &game.BetContractResolution{
			ContractVersion:     game.ContractVersionAssumedPossessionV1,
			Kind:                game.ResolutionNullifiedWrongTeam,
			NullificationReason: game.NullificationReasonNoLocation,
			Reasoning:           "accepted bet refunded because the resolving event did not expose an actual shot team",
		}, true
	}

	if strings.TrimSpace(b.ContractBinding.AssumedTeam) != "" && actualShotTeam != b.ContractBinding.AssumedTeam {
		b.Status = "nullified"
		b.NullificationReason = "wrong team shot"
		result.Outcome = "nullified"
		result.NullificationReason = b.NullificationReason
		result.Balance = e.balances.AddBalance(b.Wallet, b.Amount)
		return &game.BetContractResolution{
			ContractVersion:     game.ContractVersionAssumedPossessionV1,
			Kind:                game.ResolutionNullifiedWrongTeam,
			ActualShotTeam:      actualShotTeam,
			NullificationReason: game.NullificationReasonWrongTeam,
			Reasoning:           fmt.Sprintf("accepted under assumed team %s, but next shot came from %s", b.ContractBinding.AssumedTeam, actualShotTeam),
		}, true
	}

	if event.Location == nil {
		b.Status = "nullified"
		b.NullificationReason = "play has no location data"
		result.Outcome = "nullified"
		result.NullificationReason = b.NullificationReason
		result.Balance = e.balances.AddBalance(b.Wallet, b.Amount)
		return &game.BetContractResolution{
			ContractVersion:     game.ContractVersionAssumedPossessionV1,
			Kind:                game.ResolutionNullifiedWrongTeam,
			ActualShotTeam:      actualShotTeam,
			NullificationReason: game.NullificationReasonNoLocation,
			Reasoning:           "accepted bet refunded because the resolving shot had no location data",
		}, true
	}

	dx := b.Coordinate.X - event.Location.X
	dy := b.Coordinate.Y - event.Location.Y
	distance := math.Sqrt(dx*dx + dy*dy)
	if distance <= b.BetRadius {
		b.Status = "win"
		b.Payout = math.Round((b.Amount*2)*100) / 100
		result.Outcome = "win"
		result.AmountWon = b.Payout
		result.Balance = e.balances.AddBalance(b.Wallet, b.Payout)
		return &game.BetContractResolution{
			ContractVersion: game.ContractVersionAssumedPossessionV1,
			Kind:            game.ResolutionWin,
			ActualShotTeam:  actualShotTeam,
			Reasoning:       fmt.Sprintf("settled win under assumed team %s", b.ContractBinding.AssumedTeam),
		}, true
	}

	b.Status = "loss"
	result.Outcome = "loss"
	result.Balance = e.balances.GetBalance(b.Wallet)
	return &game.BetContractResolution{
		ContractVersion: game.ContractVersionAssumedPossessionV1,
		Kind:            game.ResolutionLoss,
		ActualShotTeam:  actualShotTeam,
		Reasoning:       fmt.Sprintf("settled loss under assumed team %s", b.ContractBinding.AssumedTeam),
	}, true
}

func (e *Engine) NextNonce(wallet [20]byte) uint64 {
	used := make(map[uint64]struct{})
	for _, b := range e.store.BetsByWallet(wallet) {
		if b != nil {
			used[b.Nonce] = struct{}{}
		}
	}
	for nonce := uint64(1); ; nonce++ {
		if _, ok := used[nonce]; !ok {
			return nonce
		}
	}
}

func (e *Engine) BetHistory(wallet [20]byte, since time.Time) []SignInAckBetHistory {
	bets := e.store.BetsByWallet(wallet)
	sort.SliceStable(bets, func(i, j int) bool {
		return bets[i].ReceivedAt.After(bets[j].ReceivedAt)
	})
	out := make([]SignInAckBetHistory, 0, len(bets))
	for _, b := range bets {
		if b == nil || b.ReceivedAt.Before(since) {
			continue
		}
		out = append(out, SignInAckBetHistory{
			BetID:               b.BetID,
			GameID:              b.GameID,
			RoundID:             b.RoundID,
			Nonce:               b.Nonce,
			Amount:              b.Amount,
			X:                   b.Coordinate.X,
			Y:                   b.Coordinate.Y,
			BetRadius:           b.BetRadius,
			MinimumMultiplier:   b.MinimumMultiplier,
			ActualMultiplier:    b.ActualMultiplier,
			Status:              b.Status,
			PlacedAt:            b.ReceivedAt.UTC().Format(time.RFC3339),
			Simulation:          b.Simulation,
			Payout:              b.Payout,
			NullificationReason: b.NullificationReason,
			RejectionReason:     b.RejectionReason,
			IsHistorical:        true,
			ContractBinding:     b.ContractBinding,
			ContractResolution:  b.ContractResolution,
		})
	}
	return out
}
