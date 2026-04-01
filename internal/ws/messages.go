package ws

import "github.com/almanac/espn-shots/internal/bet"

// Client→Server message types
const (
	MsgTypePlaceBet        = "place_bet"
	MsgTypeSubscribeGame   = "subscribe_game"
	MsgTypeSubscribeWallet = "subscribe_wallet"
)

// Server→Client message types
const (
	MsgTypePlayEvent   = "play_event"
	MsgTypeBetReceived = "bet_received"
	MsgTypeBetResult   = "bet_result"
	MsgTypeGameState   = "game_state"
	MsgTypeError       = "error"
)

// IncomingMessage is a client→server message envelope.
type IncomingMessage struct {
	Type string `json:"type"`
	// PlaceBet fields
	Bet *bet.Bet `json:"bet,omitempty"`
	// Subscribe fields
	GameID     string `json:"game_id,omitempty"`
	WalletAddr string `json:"wallet_addr,omitempty"`
}

// OutgoingMessage is a server→client message envelope.
type OutgoingMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data,omitempty"`
}

// ErrorMessage is sent when an operation fails.
type ErrorMessage struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// NewPlayEvent creates a play_event message.
func NewPlayEvent(data interface{}) *OutgoingMessage {
	return &OutgoingMessage{Type: MsgTypePlayEvent, Data: data}
}

// NewBetReceived creates a bet_received message.
func NewBetReceived(b *bet.Bet) *OutgoingMessage {
	return &OutgoingMessage{Type: MsgTypeBetReceived, Data: b}
}

// NewBetResult creates a bet_result message.
func NewBetResult(r *bet.BetResult) *OutgoingMessage {
	return &OutgoingMessage{Type: MsgTypeBetResult, Data: r}
}

// NewGameState creates a game_state message.
func NewGameState(data interface{}) *OutgoingMessage {
	return &OutgoingMessage{Type: MsgTypeGameState, Data: data}
}

// NewError creates an error message.
func NewError(msg string) *OutgoingMessage {
	return &OutgoingMessage{Type: MsgTypeError, Data: ErrorMessage{Message: msg}}
}
