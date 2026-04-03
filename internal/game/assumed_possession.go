package game

// Assumed-possession contract foundation for the ESPN-only backend truth model.
// Item 4 defines the wire/domain contract here so Item 5 can implement emission
// without inventing shapes ad hoc in the frontend or transport layer.

// ContractVersionAssumedPossessionV1 is the first explicit backend contract
// version for ESPN-only assumed-possession semantics.
const ContractVersionAssumedPossessionV1 = "assumed_possession.v1"

type MarketState string

const (
	MarketStateClosed               MarketState = "closed"
	MarketStateSettling             MarketState = "settling"
	MarketStateOpen                 MarketState = "open"
	MarketStateLowConfidence        MarketState = "low_confidence"
	MarketStateLocked               MarketState = "locked"
	MarketStateSettledWin           MarketState = "settled_win"
	MarketStateSettledLoss          MarketState = "settled_loss"
	MarketStateNullifiedWrongTeam   MarketState = "nullified_wrong_team"
	MarketStateRejectedTooLate      MarketState = "rejected_too_late"
	MarketStateRejectedMarketClosed MarketState = "rejected_market_closed"
)

type ConfidenceLevel string

const (
	ConfidenceHigh   ConfidenceLevel = "high"
	ConfidenceMedium ConfidenceLevel = "medium"
	ConfidenceLow    ConfidenceLevel = "low"
	ConfidenceNone   ConfidenceLevel = "none"
)

// AssumptionSource names the authority that produced the backend truth.
// The initial product scope is ESPN-only; this field prevents the frontend
// from assuming stronger provenance than the backend actually has.
type AssumptionSource string

const (
	AssumptionSourceESPN AssumptionSource = "espn"
)

// ResolutionKind separates accepted/refunded/nullified outcomes from rejections.
// The distinction between nullification and too-late is product-critical.
type ResolutionKind string

const (
	ResolutionPending              ResolutionKind = "pending"
	ResolutionWin                  ResolutionKind = "win"
	ResolutionLoss                 ResolutionKind = "loss"
	ResolutionNullifiedWrongTeam   ResolutionKind = "nullified_wrong_team"
	ResolutionRejectedTooLate      ResolutionKind = "rejected_too_late"
	ResolutionRejectedMarketClosed ResolutionKind = "rejected_market_closed"
	ResolutionRejectedInvalid      ResolutionKind = "rejected_invalid"
)

type NullificationReason string

const (
	NullificationReasonWrongTeam             NullificationReason = "wrong_team_shot"
	NullificationReasonNoLocation            NullificationReason = "play_missing_location"
	NullificationReasonMissingEventTimestamp NullificationReason = "play_timestamp_unavailable"
)

type TooLateReason string

const (
	TooLateReasonEventAlreadyOccurred TooLateReason = "event_already_occurred"
	TooLateReasonEventAlreadyResolved TooLateReason = "round_already_resolved"
	TooLateReasonTimestampExpired     TooLateReason = "bet_timestamp_expired"
)

// LaneKind identifies whether state belongs to the live or simulation lane.
// The frontend must treat this as authoritative instead of trying to infer the
// lane from route state, tabs, or duplicated websocket endpoints.
type LaneKind string

const (
	LaneKindLive       LaneKind = "live"
	LaneKindSimulation LaneKind = "simulation"
)

// AssumedPossessionState is the backend-authored truth the frontend should use
// to render "next TEAM shot" semantics. If nil/absent, the backend is stating
// that it does not currently have an honest assumed-possession view to expose.
type AssumedPossessionState struct {
	ContractVersion string             `json:"contractVersion"`
	Source          AssumptionSource   `json:"source"`
	MarketState     MarketState        `json:"marketState"`
	AssumedTeam     string             `json:"assumedTeam,omitempty"`
	Confidence      ConfidenceLevel    `json:"confidence"`
	Reasoning       string             `json:"reasoning,omitempty"`
	BoundGameID     string             `json:"boundGameId,omitempty"`
	BoundRoundID    string             `json:"boundRoundId,omitempty"`
	ResolvedEventID string             `json:"resolvedEventId,omitempty"`
	ActualShotTeam  string             `json:"actualShotTeam,omitempty"`
	Lane            LaneDescriptor     `json:"lane"`
	ReplayLatency   *ReplayLatencyMeta `json:"replayLatency,omitempty"`
}

// LaneDescriptor makes lane isolation explicit and gives downstream consumers a
// stable identifier for cache keys, routing, and analytics separation.
type LaneDescriptor struct {
	Kind       LaneKind `json:"kind"`
	LaneID     string   `json:"laneId"`
	Simulation bool     `json:"simulation"`
	Isolated   bool     `json:"isolated"`
}

// ReplayLatencyMeta carries the backend timing fields later simulation work
// needs in order to display replay drift honestly and avoid conflating replay
// timing with live ESPN timing.
type ReplayLatencyMeta struct {
	ReplaySourceGameID   string `json:"replaySourceGameId,omitempty"`
	ReplaySequence       int    `json:"replaySequence,omitempty"`
	SourceEventTimestamp string `json:"sourceEventTimestamp,omitempty"`
	ObservedAt           string `json:"observedAt,omitempty"`
	EmittedAt            string `json:"emittedAt,omitempty"`
	FeedLagMs            int64  `json:"feedLagMs,omitempty"`
	ReplayOffsetMs       int64  `json:"replayOffsetMs,omitempty"`
	Synthetic            bool   `json:"synthetic"`
}

// BetContractBinding records what the backend believed the bet meant at the
// time of acceptance. Later frontend work must render this binding rather than
// reconstructing intent from local UI state.
type BetContractBinding struct {
	ContractVersion string          `json:"contractVersion"`
	GameID          string          `json:"gameId"`
	RoundID         string          `json:"roundId"`
	MarketState     MarketState     `json:"marketState"`
	AssumedTeam     string          `json:"assumedTeam,omitempty"`
	Confidence      ConfidenceLevel `json:"confidence"`
	Reasoning       string          `json:"reasoning,omitempty"`
	Lane            LaneDescriptor  `json:"lane"`
}

// BetContractResolution preserves the backend's settlement/rejection truth.
// Nullification reasons and too-late reasons are intentionally separate fields.
type BetContractResolution struct {
	ContractVersion     string              `json:"contractVersion"`
	Kind                ResolutionKind      `json:"kind"`
	ActualShotTeam      string              `json:"actualShotTeam,omitempty"`
	NullificationReason NullificationReason `json:"nullificationReason,omitempty"`
	TooLateReason       TooLateReason       `json:"tooLateReason,omitempty"`
	Reasoning           string              `json:"reasoning,omitempty"`
}
