package game

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// ResolvedRoundMeta records which actual event closed a previously open round.
type ResolvedRoundMeta struct {
	RoundID         string    `json:"roundId"`
	ResolvedEventID string    `json:"resolvedEventId,omitempty"`
	ActualShotTeam  string    `json:"actualShotTeam,omitempty"`
	ResolvedAt      time.Time `json:"resolvedAt,omitempty"`
}

// BetAdmission is the tracker verdict for whether a live-money bet may bind to
// the current backend-authored assumed-possession market.
type BetAdmission struct {
	Accepted   bool
	Reason     string
	Binding    *BetContractBinding
	Resolution *BetContractResolution
}

// AssumedPossessionTracker maintains live round lineage so round-N markets can
// resolve honestly on round N+1 without the frontend inventing semantics.
type AssumedPossessionTracker struct {
	mu            sync.RWMutex
	lastEvent     map[string]PlayEvent
	currentRound  map[string]string
	resolvedRound map[string]map[string]ResolvedRoundMeta
}

func NewAssumedPossessionTracker() *AssumedPossessionTracker {
	return &AssumedPossessionTracker{
		lastEvent:     make(map[string]PlayEvent),
		currentRound:  make(map[string]string),
		resolvedRound: make(map[string]map[string]ResolvedRoundMeta),
	}
}

// OnPlay records the newly-opened live round anchored on this resolved event.
// The market for round N is resolved by the next shooting event, round N+1.
func (t *AssumedPossessionTracker) OnPlay(event PlayEvent) {
	if event.GameID == "" || event.PlayID == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if prev := t.currentRound[event.GameID]; prev != "" {
		if t.resolvedRound[event.GameID] == nil {
			t.resolvedRound[event.GameID] = make(map[string]ResolvedRoundMeta)
		}
		t.resolvedRound[event.GameID][prev] = ResolvedRoundMeta{
			RoundID:         prev,
			ResolvedEventID: event.PlayID,
			ActualShotTeam:  ActualShotTeam(event),
			ResolvedAt:      event.Timestamp.UTC(),
		}
	}

	t.lastEvent[event.GameID] = event
	t.currentRound[event.GameID] = event.PlayID
}

func (t *AssumedPossessionTracker) CurrentRound(gameID string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.currentRound[gameID]
}

func (t *AssumedPossessionTracker) ResolveInfo(gameID, roundID string) (ResolvedRoundMeta, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	meta, ok := t.resolvedRound[gameID][roundID]
	return meta, ok
}

func (t *AssumedPossessionTracker) Snapshot(state GameState) *AssumedPossessionState {
	t.mu.RLock()
	event, ok := t.lastEvent[state.GameID]
	t.mu.RUnlock()
	if !ok {
		return nil
	}
	if state.Simulation {
		return InferSimulationAssumedPossession(state, event, replayLatencyFromState(state))
	}
	return InferLiveAssumedPossession(state, event)
}

func (t *AssumedPossessionTracker) Admit(state GameState, roundID string, betTimestamp int64, expiryWindow time.Duration) BetAdmission {
	now := time.Now().Unix()
	if math.Abs(float64(now-betTimestamp)) > expiryWindow.Seconds() {
		return BetAdmission{
			Accepted: false,
			Reason:   "stale bet timestamp",
			Resolution: &BetContractResolution{
				ContractVersion: ContractVersionAssumedPossessionV1,
				Kind:            ResolutionRejectedTooLate,
				TooLateReason:   TooLateReasonTimestampExpired,
				Reasoning:       "bet timestamp expired before backend acceptance",
			},
		}
	}

	currentRound := t.CurrentRound(state.GameID)
	if currentRound == "" {
		return BetAdmission{
			Accepted: false,
			Reason:   "market closed",
			Resolution: &BetContractResolution{
				ContractVersion: ContractVersionAssumedPossessionV1,
				Kind:            ResolutionRejectedMarketClosed,
				Reasoning:       "backend has not opened an assumed-possession round for this game yet",
			},
		}
	}
	if roundID != currentRound {
		reasoning := fmt.Sprintf("bet targeted round %s after live market advanced to %s", roundID, currentRound)
		if meta, ok := t.ResolveInfo(state.GameID, roundID); ok {
			reasoning = fmt.Sprintf("round %s already resolved on event %s", roundID, meta.ResolvedEventID)
		}
		return BetAdmission{
			Accepted: false,
			Reason:   "unknown or resolved roundId",
			Resolution: &BetContractResolution{
				ContractVersion: ContractVersionAssumedPossessionV1,
				Kind:            ResolutionRejectedTooLate,
				TooLateReason:   TooLateReasonEventAlreadyResolved,
				Reasoning:       reasoning,
			},
		}
	}

	market := t.Snapshot(state)
	if market == nil || market.MarketState != MarketStateOpen || strings.TrimSpace(market.AssumedTeam) == "" {
		reasoning := "backend does not currently have an honest assumed-possession market to accept"
		if market != nil && strings.TrimSpace(market.Reasoning) != "" {
			reasoning = market.Reasoning
		}
		return BetAdmission{
			Accepted: false,
			Reason:   "market closed",
			Resolution: &BetContractResolution{
				ContractVersion: ContractVersionAssumedPossessionV1,
				Kind:            ResolutionRejectedMarketClosed,
				Reasoning:       reasoning,
			},
		}
	}

	return BetAdmission{Accepted: true, Binding: BindingFromAssumedPossession(market)}
}

func BindingFromAssumedPossession(state *AssumedPossessionState) *BetContractBinding {
	if state == nil {
		return nil
	}
	lane := state.Lane
	return &BetContractBinding{
		ContractVersion: state.ContractVersion,
		GameID:          state.BoundGameID,
		RoundID:         state.BoundRoundID,
		MarketState:     state.MarketState,
		AssumedTeam:     state.AssumedTeam,
		Confidence:      state.Confidence,
		Reasoning:       state.Reasoning,
		Lane:            lane,
	}
}

func InferLiveAssumedPossession(state GameState, event PlayEvent) *AssumedPossessionState {
	if state.GameID == "" || event.GameID == "" || state.GameID != event.GameID {
		return nil
	}
	lane := LaneDescriptor{Kind: LaneKindLive, LaneID: fmt.Sprintf("live:%s", state.GameID), Simulation: false, Isolated: false}
	return inferAssumedPossession(state, event, lane, nil)
}

func InferSimulationAssumedPossession(state GameState, event PlayEvent, replayLatency *ReplayLatencyMeta) *AssumedPossessionState {
	if state.GameID == "" || event.GameID == "" || state.GameID != event.GameID {
		return nil
	}
	sourceID := state.GameID
	if replayLatency != nil && strings.TrimSpace(replayLatency.ReplaySourceGameID) != "" {
		sourceID = replayLatency.ReplaySourceGameID
	}
	lane := LaneDescriptor{Kind: LaneKindSimulation, LaneID: fmt.Sprintf("simulation:%s", sourceID), Simulation: true, Isolated: true}
	return inferAssumedPossession(state, event, lane, replayLatency)
}

func inferAssumedPossession(state GameState, event PlayEvent, lane LaneDescriptor, replayLatency *ReplayLatencyMeta) *AssumedPossessionState {
	base := &AssumedPossessionState{
		ContractVersion: ContractVersionAssumedPossessionV1,
		Source:          AssumptionSourceESPN,
		MarketState:     MarketStateClosed,
		Confidence:      ConfidenceNone,
		BoundGameID:     state.GameID,
		BoundRoundID:    event.PlayID,
		Lane:            lane,
		ReplayLatency:   replayLatency,
	}
	if state.Completed {
		base.Reasoning = "game completed; no next-shot market remains"
		return base
	}

	shotTeam := ActualShotTeam(event)
	if shotTeam == "" {
		base.Reasoning = "latest ESPN shot is missing team identity"
		return base
	}
	opp := OpponentTeam(state, shotTeam)
	if opp == "" {
		base.Reasoning = fmt.Sprintf("could not determine opponent for shot team %s", shotTeam)
		return base
	}

	possession := strings.TrimSpace(state.Possession)
	made, hasMade := ShotMade(event)
	if hasMade && made {
		expected := opp
		if possession != "" && possession != expected {
			base.MarketState = MarketStateLowConfidence
			base.Confidence = ConfidenceLow
			base.Reasoning = fmt.Sprintf("made basket by %s, but ESPN possession currently shows %s; keeping market closed", shotTeam, possession)
			return base
		}
		base.MarketState = MarketStateOpen
		base.AssumedTeam = expected
		if possession == expected {
			base.Confidence = ConfidenceHigh
			base.Reasoning = fmt.Sprintf("made basket by %s -> inbound to %s; ESPN possession agrees", shotTeam, expected)
			return base
		}
		base.Confidence = ConfidenceMedium
		base.Reasoning = fmt.Sprintf("made basket by %s -> expected inbound to %s; ESPN possession is unavailable", shotTeam, expected)
		return base
	}

	if possession == "" {
		base.MarketState = MarketStateLowConfidence
		base.Confidence = ConfidenceLow
		base.Reasoning = fmt.Sprintf("latest shot by %s did not score and ESPN possession is unavailable", shotTeam)
		return base
	}
	if possession != state.Home && possession != state.Away {
		base.MarketState = MarketStateLowConfidence
		base.Confidence = ConfidenceLow
		base.Reasoning = fmt.Sprintf("ESPN possession value %s does not match either listed team", possession)
		return base
	}

	base.MarketState = MarketStateOpen
	base.AssumedTeam = possession
	base.Confidence = ConfidenceMedium
	if possession == shotTeam {
		base.Reasoning = fmt.Sprintf("ESPN possession remains with %s after the latest shot, so the backend keeps the next-shot market on %s", shotTeam, possession)
	} else {
		base.Reasoning = fmt.Sprintf("latest shot by %s did not score; ESPN possession now points to %s", shotTeam, possession)
	}
	return base
}

func replayLatencyFromState(state GameState) *ReplayLatencyMeta {
	if state.AssumedPossession == nil {
		return nil
	}
	return state.AssumedPossession.ReplayLatency
}

func ActualShotTeam(event PlayEvent) string {
	if data, ok := event.EventData.(map[string]any); ok {
		if team, ok := data["team"].(string); ok {
			return strings.TrimSpace(team)
		}
	}
	return ""
}

func ShotMade(event PlayEvent) (bool, bool) {
	if data, ok := event.EventData.(map[string]any); ok {
		if made, ok := data["made"].(bool); ok {
			return made, true
		}
	}
	return false, false
}

func OpponentTeam(state GameState, team string) string {
	team = strings.TrimSpace(team)
	home := strings.TrimSpace(state.Home)
	away := strings.TrimSpace(state.Away)
	switch team {
	case home:
		return away
	case away:
		return home
	default:
		return ""
	}
}
