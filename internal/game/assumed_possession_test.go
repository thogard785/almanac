package game

import (
	"encoding/json"
	"testing"
)

func TestAssumedPossessionStateJSONShape(t *testing.T) {
	state := AssumedPossessionState{
		ContractVersion: ContractVersionAssumedPossessionV1,
		Source:          AssumptionSourceESPN,
		MarketState:     MarketStateOpen,
		AssumedTeam:     "LAL",
		Confidence:      ConfidenceHigh,
		Reasoning:       "made basket by BOS -> inbound to LAL",
		BoundGameID:     "401234567",
		BoundRoundID:    "401234567_play_89",
		Lane: LaneDescriptor{
			Kind:       LaneKindLive,
			LaneID:     "live",
			Simulation: false,
			Isolated:   false,
		},
	}

	buf, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal assumed possession: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(buf, &decoded); err != nil {
		t.Fatalf("unmarshal assumed possession: %v", err)
	}

	if got := decoded["contractVersion"]; got != ContractVersionAssumedPossessionV1 {
		t.Fatalf("contractVersion = %v", got)
	}
	if got := decoded["marketState"]; got != string(MarketStateOpen) {
		t.Fatalf("marketState = %v", got)
	}
	if got := decoded["assumedTeam"]; got != "LAL" {
		t.Fatalf("assumedTeam = %v", got)
	}
	if _, ok := decoded["lane"]; !ok {
		t.Fatal("expected lane field")
	}
}

func TestBetContractResolutionDistinguishesNullificationAndTooLate(t *testing.T) {
	nullified, err := json.Marshal(BetContractResolution{
		ContractVersion:     ContractVersionAssumedPossessionV1,
		Kind:                ResolutionNullifiedWrongTeam,
		NullificationReason: NullificationReasonWrongTeam,
	})
	if err != nil {
		t.Fatalf("marshal nullified resolution: %v", err)
	}

	tooLate, err := json.Marshal(BetContractResolution{
		ContractVersion: ContractVersionAssumedPossessionV1,
		Kind:            ResolutionRejectedTooLate,
		TooLateReason:   TooLateReasonEventAlreadyOccurred,
	})
	if err != nil {
		t.Fatalf("marshal too-late resolution: %v", err)
	}

	var nullifiedDecoded map[string]any
	if err := json.Unmarshal(nullified, &nullifiedDecoded); err != nil {
		t.Fatalf("unmarshal nullified resolution: %v", err)
	}
	var tooLateDecoded map[string]any
	if err := json.Unmarshal(tooLate, &tooLateDecoded); err != nil {
		t.Fatalf("unmarshal too-late resolution: %v", err)
	}

	if got := nullifiedDecoded["nullificationReason"]; got != string(NullificationReasonWrongTeam) {
		t.Fatalf("nullificationReason = %v", got)
	}
	if _, ok := nullifiedDecoded["tooLateReason"]; ok {
		t.Fatal("nullified payload should not carry tooLateReason")
	}
	if got := tooLateDecoded["tooLateReason"]; got != string(TooLateReasonEventAlreadyOccurred) {
		t.Fatalf("tooLateReason = %v", got)
	}
	if _, ok := tooLateDecoded["nullificationReason"]; ok {
		t.Fatal("too-late payload should not carry nullificationReason")
	}
}
