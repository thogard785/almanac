package bet

// EIP-712 domain constants
const (
	EIP712Domain  = "Almanac"
	EIP712Version = "1"
	EIP712ChainID = 10143 // Monad testnet
)

// Bet represents a user's bet on a play location.
type Bet struct {
	ID         string  `json:"id"`
	Nonce      uint64  `json:"nonce"`
	Sport      string  `json:"sport"`       // "nba", "mlb", "pga"
	GameID     string  `json:"game_id"`
	PlayID     string  `json:"play_id"`
	LocationX  float64 `json:"location_x"`
	LocationY  float64 `json:"location_y"`
	Zone       string  `json:"zone"`
	WalletAddr string  `json:"wallet_addr"` // 0x... EVM address
	Signature  string  `json:"signature"`   // hex-encoded 65-byte sig
	ReceivedAt int64   `json:"received_at"` // server Unix nanoseconds
}

// BetResult contains the outcome of a resolved bet.
type BetResult struct {
	BetID         string  `json:"bet_id"`
	PlayID        string  `json:"play_id"`
	Sport         string  `json:"sport"`
	GameID        string  `json:"game_id"`
	WalletAddr    string  `json:"wallet_addr"`
	Won           bool    `json:"won"`
	ActualX       float64 `json:"actual_x"`
	ActualY       float64 `json:"actual_y"`
	ActualZone    string  `json:"actual_zone"`
	PredictedX    float64 `json:"predicted_x"`
	PredictedY    float64 `json:"predicted_y"`
	PredictedZone string  `json:"predicted_zone"`
	Distance      float64 `json:"distance"`
	WinRadius     float64 `json:"win_radius"`
	BetReceivedAt int64   `json:"bet_received_at_ns"`
	PlayTimestamp string  `json:"play_timestamp"`
	ValidBet      bool    `json:"valid_bet"`
	TimeDeltaMs   int64   `json:"time_delta_ms"`
	ProcessedAt   int64   `json:"processed_at_ns"`
}
