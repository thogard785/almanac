package ws

import "github.com/almanac/espn-shots/internal/game"

type SubscribeWalletMessage struct {
	Type       string `json:"type"`
	WalletAddr string `json:"wallet_addr"`
	Wallet     string `json:"wallet"`
}

type SignInMessage struct {
	Type       string `json:"type"`
	Wallet     string `json:"wallet"`
	Signature  string `json:"signature"`
	Timestamp  int64  `json:"timestamp"`
	Simulation bool   `json:"simulation"`
}

type PlaceBetMessage struct {
	Type              string  `json:"type"`
	GameID            string  `json:"gameId"`
	RoundID           string  `json:"roundId"`
	Amount            float64 `json:"amount"`
	Wallet            string  `json:"wallet"`
	Signature         string  `json:"signature"`
	Nonce             uint64  `json:"nonce"`
	Timestamp         int64   `json:"timestamp"`
	X                 float64 `json:"x"`
	Y                 float64 `json:"y"`
	BetRadius         float64 `json:"betRadius"`
	Simulation        bool    `json:"simulation"`
	MinimumMultiplier uint64  `json:"minimumMultiplier"`
}

type PingMessage struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
}

type PongMessage struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
}

type GameStateMessage struct {
	Type       string           `json:"type"`
	Games      []game.GameState `json:"games"`
	Simulation bool             `json:"simulation,omitempty"`
}

type PlayEventMessage struct {
	Type       string      `json:"type"`
	GameID     string      `json:"game_id"`
	PlayID     string      `json:"play_id"`
	Sport      string      `json:"sport"`
	Timestamp  string      `json:"timestamp"`
	Location   *game.Coord `json:"location"`
	Event      any         `json:"event"`
	Simulation bool        `json:"simulation,omitempty"`
}

type ErrorMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
