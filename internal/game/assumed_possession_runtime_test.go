package game

import (
	"testing"
	"time"
)

func TestInferLiveAssumedPossessionMadeBasket(t *testing.T) {
	state := GameState{GameID: "401", Sport: "nba", Home: "BOS", Away: "LAL", Possession: "LAL"}
	event := PlayEvent{
		GameID:    "401",
		PlayID:    "p1",
		Timestamp: time.Now().UTC(),
		EventData: map[string]any{"team": "BOS", "made": true},
	}

	snapshot := InferLiveAssumedPossession(state, event)
	if snapshot == nil {
		t.Fatal("expected snapshot")
	}
	if snapshot.MarketState != MarketStateOpen {
		t.Fatalf("market state = %s", snapshot.MarketState)
	}
	if snapshot.AssumedTeam != "LAL" {
		t.Fatalf("assumed team = %s", snapshot.AssumedTeam)
	}
	if snapshot.Confidence != ConfidenceHigh {
		t.Fatalf("confidence = %s", snapshot.Confidence)
	}
	if snapshot.Lane.Kind != LaneKindLive || snapshot.Lane.Simulation {
		t.Fatalf("unexpected lane: %+v", snapshot.Lane)
	}
}

func TestInferLiveAssumedPossessionContradictoryPossessionClosesMarket(t *testing.T) {
	state := GameState{GameID: "401", Sport: "nba", Home: "BOS", Away: "LAL", Possession: "BOS"}
	event := PlayEvent{
		GameID:    "401",
		PlayID:    "p1",
		Timestamp: time.Now().UTC(),
		EventData: map[string]any{"team": "BOS", "made": true},
	}

	snapshot := InferLiveAssumedPossession(state, event)
	if snapshot == nil {
		t.Fatal("expected snapshot")
	}
	if snapshot.MarketState != MarketStateLowConfidence {
		t.Fatalf("market state = %s", snapshot.MarketState)
	}
	if snapshot.AssumedTeam != "" {
		t.Fatalf("expected empty assumed team, got %s", snapshot.AssumedTeam)
	}
}

func TestInferSimulationAssumedPossessionCarriesReplayLaneMetadata(t *testing.T) {
	state := GameState{GameID: "sim:401", Sport: "nba", Home: "BOS", Away: "LAL", Possession: "LAL", Simulation: true}
	event := PlayEvent{
		GameID:    "sim:401",
		PlayID:    "sim:401:p1",
		Timestamp: time.Now().UTC(),
		EventData: map[string]any{"team": "BOS", "made": true},
	}
	replay := &ReplayLatencyMeta{ReplaySourceGameID: "401", ReplayOffsetMs: 15000, Synthetic: true}

	snapshot := InferSimulationAssumedPossession(state, event, replay)
	if snapshot == nil {
		t.Fatal("expected snapshot")
	}
	if snapshot.Lane.Kind != LaneKindSimulation || !snapshot.Lane.Isolated || !snapshot.Lane.Simulation {
		t.Fatalf("unexpected lane: %+v", snapshot.Lane)
	}
	if snapshot.ReplayLatency == nil || snapshot.ReplayLatency.ReplaySourceGameID != "401" {
		t.Fatalf("unexpected replay metadata: %+v", snapshot.ReplayLatency)
	}
}

func TestTrackerAdmitTooLateAndBinding(t *testing.T) {
	tracker := NewAssumedPossessionTracker()
	state := GameState{GameID: "401", Sport: "nba", Home: "BOS", Away: "LAL", Possession: "LAL"}
	event := PlayEvent{
		GameID:    "401",
		PlayID:    "round-1",
		Timestamp: time.Now().UTC(),
		EventData: map[string]any{"team": "BOS", "made": true},
	}
	tracker.OnPlay(event)

	admission := tracker.Admit(state, "round-1", time.Now().Unix(), 30*time.Second)
	if !admission.Accepted {
		t.Fatalf("expected accepted admission, got %+v", admission)
	}
	if admission.Binding == nil || admission.Binding.AssumedTeam != "LAL" {
		t.Fatalf("unexpected binding: %+v", admission.Binding)
	}

	nextEvent := PlayEvent{
		GameID:    "401",
		PlayID:    "round-2",
		Timestamp: time.Now().UTC().Add(2 * time.Second),
		EventData: map[string]any{"team": "LAL", "made": false},
	}
	tracker.OnPlay(nextEvent)

	tooLate := tracker.Admit(state, "round-1", time.Now().Unix(), 30*time.Second)
	if tooLate.Accepted {
		t.Fatal("expected too-late rejection")
	}
	if tooLate.Resolution == nil || tooLate.Resolution.Kind != ResolutionRejectedTooLate {
		t.Fatalf("unexpected too-late resolution: %+v", tooLate.Resolution)
	}
}
