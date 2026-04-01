package ws

import "github.com/almanac/espn-shots/internal/game"

type IdentifyMessage struct {
	Type   string `json:"type"`
	Wallet string `json:"wallet"`
}

type PlaceBetMessage struct {
	Type       string     `json:"type"`
	GameID     string     `json:"game_id"`
	PlayID     string     `json:"play_id"`
	Coordinate game.Coord `json:"coordinate"`
	Amount     float64    `json:"amount"`
	Wallet     string     `json:"wallet"`
	Signature  string     `json:"signature"`
	Nonce      uint64     `json:"nonce"`
}

type GameStateMessage struct {
	Type  string           `json:"type"`
	Games []game.GameState `json:"games"`
}

type PlayEventMessage struct {
	Type      string      `json:"type"`
	GameID    string      `json:"game_id"`
	PlayID    string      `json:"play_id"`
	Sport     string      `json:"sport"`
	Timestamp string      `json:"timestamp"`
	Location  *game.Coord `json:"location"`
	Event     any         `json:"event"`
}

type BetErrorMessage struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}
