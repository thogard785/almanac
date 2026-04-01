package bet

import (
	"time"

	"github.com/almanac/espn-shots/internal/game"
)

const (
	DomainName    = "Almanac"
	DomainVersion = "1"
	DomainChainID = uint64(1)
	WinRadius     = 35.0
)

// Bet is the validated backend record for a user's wager.
type Bet struct {
	BetID         string     `json:"bet_id"`
	Wallet        [20]byte   `json:"wallet"`
	GameID        string     `json:"game_id"`
	PlayID        string     `json:"play_id"`
	Coordinate    game.Coord `json:"coordinate"`
	Amount        float64    `json:"amount"`
	Nonce         uint64     `json:"nonce"`
	ReceivedAt    time.Time  `json:"received_at"`
	Signature     []byte     `json:"signature"`
	Status        string     `json:"status"`
	InvalidReason string     `json:"invalid_reason,omitempty"`
}

// BetResult is routed only to the wallet's identified connections.
type BetResult struct {
	Type         string      `json:"type"`
	BetID        string      `json:"bet_id"`
	PlayID       string      `json:"play_id"`
	GameID       string      `json:"game_id"`
	Result       string      `json:"result"`
	Payout       float64     `json:"payout"`
	Coordinate   game.Coord  `json:"coordinate"`
	PlayLocation *game.Coord `json:"play_location"`
	Distance     float64     `json:"distance"`
	Reason       string      `json:"reason"`
}

// BalanceUpdate is currently a TODO stub but part of the public WS protocol.
type BalanceUpdate struct {
	Type    string  `json:"type"`
	Wallet  string  `json:"wallet"`
	Balance float64 `json:"balance"`
}

func getUserBalance(wallet [20]byte) float64 {
	// TODO: implement real balance tracking
	_ = wallet
	return 0.0
}
