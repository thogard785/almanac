package bet

import (
	"sync"
	"time"

	"github.com/almanac/espn-shots/internal/game"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	DomainName         = "Almanac"
	DomainVersion      = "1"
	DomainChainID      = uint64(143)
	WinRadius          = 35.0
	MinimumBetAmount   = 1.0
	ActualMultiplier   = uint64(1)
	SignInExpiryWindow = int64(60)
	BetExpiryWindow    = int64(30)
)

var SignInTypeHash = crypto.Keccak256Hash([]byte("SignIn(address wallet,uint256 timestamp,bool simulation)"))

// BalanceProvider abstracts balance storage so regular and simulation modes
// can each have fully isolated balance state.
type BalanceProvider interface {
	GetBalance(wallet [20]byte) float64
	AddBalance(wallet [20]byte, delta float64) float64
	SetBalance(wallet [20]byte, balance float64)
}

// ---------- default (regular-mode) balance provider ----------

var balanceState = struct {
	mu       sync.RWMutex
	balances map[[20]byte]float64
}{balances: make(map[[20]byte]float64)}

type defaultBalanceProvider struct{}

func (defaultBalanceProvider) GetBalance(wallet [20]byte) float64 { return getUserBalance(wallet) }
func (defaultBalanceProvider) AddBalance(wallet [20]byte, delta float64) float64 {
	return addUserBalance(wallet, delta)
}
func (defaultBalanceProvider) SetBalance(wallet [20]byte, balance float64) {
	setUserBalance(wallet, balance)
}

// DefaultBalanceProvider is the regular-mode balance provider backed by the
// package-level balanceState map.
var DefaultBalanceProvider BalanceProvider = defaultBalanceProvider{}

// Bet is the validated backend record for a user's wager.
type Bet struct {
	BetID               string     `json:"bet_id"`
	Wallet              [20]byte   `json:"wallet"`
	GameID              string     `json:"game_id"`
	RoundID             string     `json:"round_id"`
	Coordinate          game.Coord `json:"coordinate"`
	Amount              float64    `json:"amount"`
	Nonce               uint64     `json:"nonce"`
	Timestamp           int64      `json:"timestamp"`
	BetRadius           float64    `json:"bet_radius"`
	Simulation          bool       `json:"simulation"`
	MinimumMultiplier   uint64     `json:"minimum_multiplier"`
	ActualMultiplier    uint64     `json:"actual_multiplier"`
	ReceivedAt          time.Time  `json:"received_at"`
	Signature           []byte     `json:"signature"`
	Status              string     `json:"status"`
	Payout              float64    `json:"payout"`
	RejectionReason     string     `json:"rejection_reason,omitempty"`
	NullificationReason string     `json:"nullification_reason,omitempty"`
}

type SignInAck struct {
	Type             string                `json:"type"`
	Wallet           string                `json:"wallet"`
	Balance          float64               `json:"balance"`
	Simulation       bool                  `json:"simulation"`
	NextNonce        uint64                `json:"nextNonce"`
	MinimumBetAmount float64               `json:"minimumBetAmount"`
	GameRadii        map[string]float64    `json:"gameRadii"`
	BetHistory       []SignInAckBetHistory `json:"betHistory"`
}

type SignInAckBetHistory struct {
	BetID               string  `json:"betId"`
	GameID              string  `json:"gameId"`
	RoundID             string  `json:"roundId"`
	Nonce               uint64  `json:"nonce"`
	Amount              float64 `json:"amount"`
	X                   float64 `json:"x"`
	Y                   float64 `json:"y"`
	BetRadius           float64 `json:"betRadius"`
	MinimumMultiplier   uint64  `json:"minimumMultiplier"`
	ActualMultiplier    uint64  `json:"actualMultiplier"`
	Status              string  `json:"status"`
	PlacedAt            string  `json:"placedAt"`
	Simulation          bool    `json:"simulation"`
	Payout              float64 `json:"payout"`
	NullificationReason string  `json:"nullificationReason"`
	RejectionReason     string  `json:"rejectionReason"`
	IsHistorical        bool    `json:"isHistorical"`
}

type BetAck struct {
	Type             string  `json:"type"`
	Status           string  `json:"status"`
	GameID           string  `json:"gameId"`
	Nonce            uint64  `json:"nonce"`
	Timestamp        int64   `json:"timestamp"`
	Balance          float64 `json:"balance"`
	ActualMultiplier uint64  `json:"actualMultiplier"`
	RejectionReason  string  `json:"rejectionReason"`
	Simulation       bool    `json:"simulation"`
}

// BetResult is routed only to the wallet's identified connections.
type BetResult struct {
	Type                string     `json:"type"`
	Outcome             string     `json:"outcome"`
	Wallet              string     `json:"wallet"`
	Nonce               uint64     `json:"nonce"`
	GameID              string     `json:"gameId"`
	RoundID             string     `json:"roundId"`
	BetCoordinates      game.Coord `json:"betCoordinates"`
	BetRadius           float64    `json:"betRadius"`
	BackendTimestamp    int64      `json:"backendTimestamp"`
	EventTimestamp      int64      `json:"eventTimestamp"`
	AmountBet           float64    `json:"amountBet"`
	AmountWon           float64    `json:"amountWon"`
	Simulation          bool       `json:"simulation"`
	Balance             float64    `json:"balance"`
	IsHistorical        bool       `json:"isHistorical"`
	NullificationReason string     `json:"nullificationReason"`
}

func setUserBalance(wallet [20]byte, balance float64) {
	balanceState.mu.Lock()
	balanceState.balances[wallet] = balance
	balanceState.mu.Unlock()
}

func addUserBalance(wallet [20]byte, delta float64) float64 {
	balanceState.mu.Lock()
	balanceState.balances[wallet] += delta
	balance := balanceState.balances[wallet]
	balanceState.mu.Unlock()
	return balance
}

func CurrentBalance(wallet [20]byte) float64 {
	return getUserBalance(wallet)
}

// TODO: on-chain payment processing — frontend signs only, backend handles chain.
func getUserBalance(wallet [20]byte) float64 {
	balanceState.mu.RLock()
	balance, ok := balanceState.balances[wallet]
	balanceState.mu.RUnlock()
	if !ok {
		return 0.0
	}
	return balance
}
