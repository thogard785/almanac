package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/almanac/espn-shots/internal/bet"
	"github.com/almanac/espn-shots/internal/espn"
	"github.com/almanac/espn-shots/internal/game"
	ws "github.com/almanac/espn-shots/internal/ws"
	"github.com/gorilla/websocket"
)

func main() {
	cfg := loadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app, err := NewApp(cfg)
	if err != nil {
		log.Fatalf("init app: %v", err)
	}
	app.Start(ctx)

	mux := http.NewServeMux()
	wsHandler := app.hub.HandleWS(app.handleConnect, app.handleDisconnect, app.handleMessage)
	mux.HandleFunc("/ws", wsHandler)
	mux.HandleFunc("/almanac/ws", wsHandler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	port := os.Getenv("WS_PORT")
	if port == "" {
		port = "8090"
	}
	srv := &http.Server{Addr: ":" + port, Handler: mux}
	go func() {
		log.Printf("[almanac] listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[almanac] server error: %v", err)
		}
	}()

	<-ctx.Done()
	app.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func loadConfig() Config {
	var cfg Config
	flag.DurationVar(&cfg.PollInterval, "poll-interval", 3*time.Second, "poll interval")
	flag.Parse()
	return cfg
}

type App struct {
	hub      *Hub
	manager  *espn.Manager
	betStore *bet.Store
	bets     *bet.Engine

	persistDone chan struct{}

	mu             sync.RWMutex
	connWallets    map[*websocket.Conn][20]byte
	walletConns    map[[20]byte]map[*websocket.Conn]struct{}
	connSimulation map[*websocket.Conn]bool
}

func NewApp(cfg Config) (*App, error) {
	store, err := bet.NewStore("data/bets")
	if err != nil {
		return nil, err
	}
	client := espn.NewClient(350 * time.Millisecond)
	app := &App{
		hub:            NewHub(),
		betStore:       store,
		bets:           bet.NewEngine(store),
		persistDone:    make(chan struct{}),
		connWallets:    make(map[*websocket.Conn][20]byte),
		walletConns:    make(map[[20]byte]map[*websocket.Conn]struct{}),
		connSimulation: make(map[*websocket.Conn]bool),
	}
	app.manager = espn.NewManager(client, cfg.PollInterval, app.handlePlayEvent)
	return app, nil
}

func (a *App) Start(ctx context.Context) {
	go a.betStore.RunPersistLoop(a.persistDone)
	go a.bets.Run(ctx)
	a.manager.Run(ctx)
	go a.forwardBetResults(ctx)
	go a.forwardBalanceUpdates(ctx)
	go a.broadcastGameStates(ctx)
}

func (a *App) Stop() {
	select {
	case <-a.persistDone:
	default:
		close(a.persistDone)
	}
}

func (a *App) handleConnect(conn *websocket.Conn) {
	a.sendGameState(conn)
}

func (a *App) handleDisconnect(conn *websocket.Conn) {
	a.mu.Lock()
	delete(a.connSimulation, conn)
	if wallet, ok := a.connWallets[conn]; ok {
		delete(a.connWallets, conn)
		if conns := a.walletConns[wallet]; conns != nil {
			delete(conns, conn)
			if len(conns) == 0 {
				delete(a.walletConns, wallet)
			}
		}
	}
	a.mu.Unlock()
}

func (a *App) handleMessage(conn *websocket.Conn, payload []byte) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		a.sendError(conn, "invalid message")
		return
	}

	switch envelope.Type {
	case "signin":
		var msg ws.SignInMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			a.sendError(conn, "invalid signin signature")
			a.hub.Unregister(conn)
			return
		}
		a.handleSignIn(conn, msg)
	case "subscribe_wallet":
		var msg ws.SubscribeWalletMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			a.sendError(conn, "invalid wallet")
			return
		}
		walletValue := msg.WalletAddr
		if walletValue == "" {
			walletValue = msg.Wallet
		}
		wallet, err := bet.ParseWallet(walletValue)
		if err != nil {
			a.sendError(conn, "invalid wallet")
			return
		}
		a.identifyConnection(conn, wallet, false)
		_ = a.hub.SendTo(conn, bet.BalanceUpdate{Type: "balance_update", Wallet: bet.WalletHex(wallet), Balance: 0.0})
	case "place_bet":
		var msg ws.PlaceBetMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			a.sendError(conn, "invalid place_bet payload")
			return
		}
		a.placeBet(conn, msg)
	case "ping":
		var msg ws.PingMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			return
		}
		_ = a.hub.SendTo(conn, ws.PongMessage{Type: "pong", Timestamp: msg.Timestamp})
	default:
		a.sendError(conn, fmt.Sprintf("unsupported message type %q", envelope.Type))
	}
}

func (a *App) handleSignIn(conn *websocket.Conn, msg ws.SignInMessage) {
	wallet, err := bet.ParseWallet(msg.Wallet)
	if err != nil {
		a.sendError(conn, "invalid signin signature")
		a.hub.Unregister(conn)
		return
	}
	signature, err := bet.ParseSignature(msg.Signature)
	if err != nil || bet.VerifySignInSignature(wallet, msg.Timestamp, msg.Simulation, signature) != nil {
		a.sendError(conn, "invalid signin signature")
		a.hub.Unregister(conn)
		return
	}
	if abs64(time.Now().Unix()-msg.Timestamp) > bet.SignInExpiryWindow || msg.Simulation {
		a.sendError(conn, "invalid signin signature")
		a.hub.Unregister(conn)
		return
	}
	a.identifyConnection(conn, wallet, msg.Simulation)
	ack := bet.SignInAck{
		Type:             "signin_ack",
		Wallet:           bet.WalletHex(wallet),
		Balance:          bet.CurrentBalance(wallet),
		NextNonce:        a.bets.NextNonce(wallet),
		MinimumBetAmount: bet.MinimumBetAmount,
		GameRadii:        a.currentGameRadii(),
		BetHistory:       a.bets.BetHistory(wallet, time.Now().AddDate(0, 0, -7)),
	}
	_ = a.hub.SendTo(conn, ack)
}

func (a *App) placeBet(conn *websocket.Conn, msg ws.PlaceBetMessage) {
	wallet, err := bet.ParseWallet(msg.Wallet)
	if err != nil {
		a.sendError(conn, "invalid wallet")
		return
	}
	signature, err := bet.ParseSignature(msg.Signature)
	if err != nil {
		a.sendError(conn, err.Error())
		return
	}

	a.mu.RLock()
	identified, hasIdentity := a.connWallets[conn]
	connSimulation := a.connSimulation[conn]
	a.mu.RUnlock()
	if hasIdentity && identified != wallet {
		a.sendError(conn, "wallet does not match identified connection")
		return
	}
	if connSimulation || msg.Simulation {
		ack, _ := a.bets.PlaceBet(&bet.Bet{Wallet: wallet, GameID: msg.GameID, RoundID: msg.RoundID, Amount: msg.Amount, Nonce: msg.Nonce, Timestamp: msg.Timestamp, Coordinate: game.Coord{X: msg.X, Y: msg.Y}, BetRadius: msg.BetRadius, Simulation: true, MinimumMultiplier: msg.MinimumMultiplier, Signature: signature})
		_ = a.hub.SendTo(conn, ack)
		return
	}

	b := &bet.Bet{
		Wallet:            wallet,
		GameID:            msg.GameID,
		RoundID:           msg.RoundID,
		Coordinate:        game.Coord{X: msg.X, Y: msg.Y},
		Amount:            msg.Amount,
		Nonce:             msg.Nonce,
		Timestamp:         msg.Timestamp,
		BetRadius:         msg.BetRadius,
		Simulation:        msg.Simulation,
		MinimumMultiplier: msg.MinimumMultiplier,
		Signature:         signature,
	}
	ack, err := a.bets.PlaceBet(b)
	if err != nil {
		a.sendError(conn, err.Error())
		return
	}
	a.identifyConnection(conn, wallet, msg.Simulation)
	_ = a.hub.SendTo(conn, ack)
}

func (a *App) identifyConnection(conn *websocket.Conn, wallet [20]byte, simulation bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if prev, ok := a.connWallets[conn]; ok && prev != wallet {
		if conns := a.walletConns[prev]; conns != nil {
			delete(conns, conn)
			if len(conns) == 0 {
				delete(a.walletConns, prev)
			}
		}
	}
	a.connWallets[conn] = wallet
	a.connSimulation[conn] = simulation
	if a.walletConns[wallet] == nil {
		a.walletConns[wallet] = make(map[*websocket.Conn]struct{})
	}
	a.walletConns[wallet][conn] = struct{}{}
}

func (a *App) handlePlayEvent(event game.PlayEvent) {
	a.bets.OnPlayEvent(event)
	a.hub.Broadcast(ws.PlayEventMessage{
		Type:      "play_event",
		GameID:    event.GameID,
		PlayID:    event.PlayID,
		Sport:     event.Sport,
		Timestamp: event.Timestamp.UTC().Format(time.RFC3339),
		Location:  event.Location,
		Event:     event.EventData,
	})
}

func (a *App) forwardBetResults(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-a.bets.ResultChan():
			a.sendToWallet(item.Wallet, item.Result)
		}
	}
}

func (a *App) forwardBalanceUpdates(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case update := <-a.bets.BalanceChan():
			wallet, err := bet.ParseWallet(update.Wallet)
			if err != nil {
				continue
			}
			a.sendToWallet(wallet, update)
		}
	}
}

func (a *App) broadcastGameStates(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	a.hub.Broadcast(a.currentGameStateMessage())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.hub.Broadcast(a.currentGameStateMessage())
		}
	}
}

func (a *App) currentGameStateMessage() ws.GameStateMessage {
	games := a.filteredGames()
	sort.SliceStable(games, func(i, j int) bool {
		if games[i].Sport == games[j].Sport {
			return games[i].GameID < games[j].GameID
		}
		return games[i].Sport < games[j].Sport
	})
	return ws.GameStateMessage{Type: "game_state", Games: games}
}

func (a *App) filteredGames() []game.GameState {
	games := a.manager.Games()
	cutoff := time.Now().Add(-24 * time.Hour)
	filtered := make([]game.GameState, 0, len(games))
	for _, g := range games {
		if g.StartTime != "" {
			if start := game.ParseESPNTime(g.StartTime); !start.IsZero() && start.Before(cutoff) {
				continue
			}
		}
		filtered = append(filtered, g)
	}
	return filtered
}

func (a *App) currentGameRadii() map[string]float64 {
	games := a.filteredGames()
	out := make(map[string]float64, len(games))
	for _, g := range games {
		out[g.GameID] = bet.WinRadius
	}
	return out
}

func (a *App) sendGameState(conn *websocket.Conn) {
	_ = a.hub.SendTo(conn, a.currentGameStateMessage())
}

func (a *App) sendError(conn *websocket.Conn, reason string) {
	_ = a.hub.SendTo(conn, ws.ErrorMessage{Type: "error", Message: reason})
}

func (a *App) sendToWallet(wallet [20]byte, msg any) {
	a.mu.RLock()
	conns := a.walletConns[wallet]
	snapshot := make([]*websocket.Conn, 0, len(conns))
	for conn := range conns {
		snapshot = append(snapshot, conn)
	}
	a.mu.RUnlock()
	for _, conn := range snapshot {
		_ = a.hub.SendTo(conn, msg)
	}
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
