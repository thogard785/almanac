package bet

import (
	"crypto/ecdsa"
	"testing"
	"time"

	"github.com/almanac/espn-shots/internal/game"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestEngineNextRoundResolutionUsesFollowingEvent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store).EnableNextRoundResolution()

	key, wallet := newSignedWallet(t)
	DefaultBalanceProvider.SetBalance(wallet, 100)

	first := game.PlayEvent{
		GameID:    "401",
		PlayID:    "round-1",
		Timestamp: time.Now().UTC().Add(-5 * time.Second),
		Location:  &game.Coord{X: 120, Y: 90},
		EventData: map[string]any{"team": "BOS", "made": true},
	}
	engine.OnPlayEvent(first)

	betTime := time.Now().Unix()
	b := &Bet{
		Wallet:     wallet,
		GameID:     "401",
		RoundID:    "round-1",
		Coordinate: game.Coord{X: 10, Y: 10},
		Amount:     5,
		Nonce:      1,
		Timestamp:  betTime,
		BetRadius:  20,
		ContractBinding: &game.BetContractBinding{
			ContractVersion: game.ContractVersionAssumedPossessionV1,
			GameID:          "401",
			RoundID:         "round-1",
			MarketState:     game.MarketStateOpen,
			AssumedTeam:     "LAL",
			Confidence:      game.ConfidenceHigh,
			Reasoning:       "made basket by BOS -> inbound to LAL",
			Lane:            game.LaneDescriptor{Kind: game.LaneKindLive, LaneID: "live:401"},
		},
	}
	signBetForTest(t, key, b)
	ack, err := engine.PlaceBet(b)
	if err != nil {
		t.Fatal(err)
	}
	if ack.Status != "accepted" {
		t.Fatalf("ack status = %s", ack.Status)
	}

	second := game.PlayEvent{
		GameID:    "401",
		PlayID:    "round-2",
		Timestamp: time.Now().UTC().Add(2 * time.Second),
		Location:  &game.Coord{X: 10, Y: 10},
		EventData: map[string]any{"team": "LAL", "made": false},
	}
	engine.OnPlayEvent(second)

	select {
	case result := <-engine.ResultChan():
		if result.Result.Outcome != "win" {
			t.Fatalf("outcome = %s", result.Result.Outcome)
		}
		if result.Result.ContractResolution == nil || result.Result.ContractResolution.Kind != game.ResolutionWin {
			t.Fatalf("unexpected contract resolution: %+v", result.Result.ContractResolution)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for result")
	}
}

func TestEngineWrongTeamNullifiesAcceptedBet(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store).EnableNextRoundResolution()

	key, wallet := newSignedWallet(t)
	DefaultBalanceProvider.SetBalance(wallet, 100)
	before := DefaultBalanceProvider.GetBalance(wallet)

	engine.OnPlayEvent(game.PlayEvent{
		GameID:    "401",
		PlayID:    "round-1",
		Timestamp: time.Now().UTC().Add(-5 * time.Second),
		Location:  &game.Coord{X: 50, Y: 50},
		EventData: map[string]any{"team": "BOS", "made": true},
	})

	b := &Bet{
		Wallet:     wallet,
		GameID:     "401",
		RoundID:    "round-1",
		Coordinate: game.Coord{X: 10, Y: 10},
		Amount:     7,
		Nonce:      1,
		Timestamp:  time.Now().Unix(),
		BetRadius:  15,
		ContractBinding: &game.BetContractBinding{
			ContractVersion: game.ContractVersionAssumedPossessionV1,
			GameID:          "401",
			RoundID:         "round-1",
			MarketState:     game.MarketStateOpen,
			AssumedTeam:     "LAL",
			Confidence:      game.ConfidenceHigh,
			Reasoning:       "made basket by BOS -> inbound to LAL",
			Lane:            game.LaneDescriptor{Kind: game.LaneKindLive, LaneID: "live:401"},
		},
	}
	signBetForTest(t, key, b)
	ack, err := engine.PlaceBet(b)
	if err != nil {
		t.Fatal(err)
	}
	if ack.Status != "accepted" {
		t.Fatalf("ack status = %s", ack.Status)
	}

	engine.OnPlayEvent(game.PlayEvent{
		GameID:    "401",
		PlayID:    "round-2",
		Timestamp: time.Now().UTC().Add(2 * time.Second),
		Location:  &game.Coord{X: 250, Y: 200},
		EventData: map[string]any{"team": "BOS", "made": false},
	})

	select {
	case result := <-engine.ResultChan():
		if result.Result.Outcome != "nullified" {
			t.Fatalf("outcome = %s", result.Result.Outcome)
		}
		if result.Result.ContractResolution == nil || result.Result.ContractResolution.Kind != game.ResolutionNullifiedWrongTeam {
			t.Fatalf("unexpected contract resolution: %+v", result.Result.ContractResolution)
		}
		if DefaultBalanceProvider.GetBalance(wallet) != before {
			t.Fatalf("expected full refund to restore balance %.2f, got %.2f", before, DefaultBalanceProvider.GetBalance(wallet))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for result")
	}
}

func newSignedWallet(t *testing.T) (*ecdsa.PrivateKey, [20]byte) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	var wallet [20]byte
	copy(wallet[:], crypto.PubkeyToAddress(key.PublicKey).Bytes())
	return key, wallet
}

func signBetForTest(t *testing.T, key *ecdsa.PrivateKey, b *Bet) {
	t.Helper()
	digest := eip712Digest(domainSeparator(), betStructHash(b))
	sig, err := crypto.Sign(digest.Bytes(), key)
	if err != nil {
		t.Fatal(err)
	}
	b.Signature = sig
}
