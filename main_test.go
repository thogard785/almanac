package main

import (
	"path/filepath"
	"testing"

	"github.com/almanac/espn-shots/internal/bet"
	ws "github.com/almanac/espn-shots/internal/ws"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/websocket"
)

func newTestApp(t *testing.T) *App {
	t.Helper()

	store, err := bet.NewStore(filepath.Join(t.TempDir(), "bets"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	return &App{
		hub:            NewHub(),
		betStore:       store,
		bets:           bet.NewEngine(store),
		connWallets:    make(map[*websocket.Conn][20]byte),
		walletConns:    make(map[[20]byte]map[*websocket.Conn]struct{}),
		connSimulation: make(map[*websocket.Conn]bool),
		connIdentified: make(map[*websocket.Conn]bool),
	}
}

func TestUnidentifiedConnectionDoesNotBelongToAnyMode(t *testing.T) {
	app := newTestApp(t)
	conn := &websocket.Conn{}

	if app.isRegularConn(conn) {
		t.Fatal("unidentified connection should not receive regular traffic")
	}
	if app.isSimConn(conn) {
		t.Fatal("unidentified connection should not receive simulation traffic")
	}
}

func TestIdentifyConnectionMarksWalletAndMode(t *testing.T) {
	app := newTestApp(t)
	conn := &websocket.Conn{}
	wallet, err := bet.ParseWallet(common.BytesToAddress([]byte("mode-test-wallet-1234")).Hex())
	if err != nil {
		t.Fatalf("parse wallet: %v", err)
	}

	app.identifyConnection(conn, wallet, true)

	if !app.isSimConn(conn) {
		t.Fatal("identified simulation connection should receive simulation traffic")
	}
	if app.isRegularConn(conn) {
		t.Fatal("identified simulation connection should not receive regular traffic")
	}
	if !app.connIdentified[conn] {
		t.Fatal("connection should be marked identified after signin")
	}
	if _, ok := app.walletConns[wallet][conn]; !ok {
		t.Fatal("wallet connection index missing identified connection")
	}
}

func TestPlaceBetRequiresSignedSignInFirst(t *testing.T) {
	app := newTestApp(t)
	conn := &websocket.Conn{}
	walletHex := common.BytesToAddress([]byte("place-bet-wallet-1234")).Hex()

	app.placeBet(conn, betMessage(walletHex, false))

	if got := len(app.betStore.AllBets()); got != 0 {
		t.Fatalf("expected no bet persistence before signin, got %d", got)
	}
	if app.connIdentified[conn] {
		t.Fatal("place_bet must not implicitly identify a connection")
	}
}

func betMessage(wallet string, simulation bool) ws.PlaceBetMessage {
	return ws.PlaceBetMessage{
		Type:              "place_bet",
		GameID:            "game-1",
		RoundID:           "round-1",
		Amount:            5,
		Wallet:            wallet,
		Signature:         "0x" + zeroSignatureHex,
		Nonce:             1,
		Timestamp:         1,
		X:                 1,
		Y:                 1,
		BetRadius:         35,
		Simulation:        simulation,
		MinimumMultiplier: 1,
	}
}

const zeroSignatureHex = "0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
