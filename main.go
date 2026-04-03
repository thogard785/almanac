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
	"github.com/almanac/espn-shots/internal/sim"
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
	sim      *sim.Manager
	tracker  *game.AssumedPossessionTracker

	persistDone    chan struct{}
	simPersistDone chan struct{}
	appCtx         context.Context

	mu             sync.RWMutex
	connWallets    map[*websocket.Conn][20]byte
	walletConns    map[[20]byte]map[*websocket.Conn]struct{}
	connSimulation map[*websocket.Conn]bool
	connIdentified map[*websocket.Conn]bool
}

func NewApp(cfg Config) (*App, error) {
	store, err := bet.NewStore("data/bets")
	if err != nil {
		return nil, err
	}
	simMgr, err := sim.NewManager()
	if err != nil {
		return nil, fmt.Errorf("init sim manager: %w", err)
	}
	client := espn.NewClient(350 * time.Millisecond)
	app := &App{
		hub:            NewHub(),
		betStore:       store,
		bets:           bet.NewEngine(store).EnableNextRoundResolution(),
		sim:            simMgr,
		tracker:        game.NewAssumedPossessionTracker(),
		persistDone:    make(chan struct{}),
		simPersistDone: make(chan struct{}),
		connWallets:    make(map[*websocket.Conn][20]byte),
		walletConns:    make(map[[20]byte]map[*websocket.Conn]struct{}),
		connSimulation: make(map[*websocket.Conn]bool),
		connIdentified: make(map[*websocket.Conn]bool),
	}
	app.manager = espn.NewManager(client, cfg.PollInterval, app.handlePlayEvent)
	// Wire ESPN game completion to save games for sim replay.
	app.manager.SetCompletionHandler(simMgr.SaveCompletedGame)
	// Wire sim replay events to sim connections.
	simMgr.EventHandler = app.handleSimPlayEvent
	return app, nil
}

func (a *App) Start(ctx context.Context) {
	a.appCtx = ctx
	go a.betStore.RunPersistLoop(a.persistDone)
	go a.sim.Store.RunPersistLoop(a.simPersistDone)
	go a.bets.Run(ctx)
	go a.sim.Engine.Run(ctx)
	a.sim.Start(ctx)
	a.manager.Run(ctx)
	go a.forwardBetResults(ctx)
	go a.forwardBalanceUpdates(ctx)
	go a.forwardSimBetResults(ctx)
	go a.forwardSimBalanceUpdates(ctx)
	go a.broadcastGameStates(ctx)
	go a.broadcastSimGameStates(ctx)
}

func (a *App) Stop() {
	a.sim.Stop()
	select {
	case <-a.persistDone:
	default:
		close(a.persistDone)
	}
	select {
	case <-a.simPersistDone:
	default:
		close(a.simPersistDone)
	}
}

func (a *App) handleConnect(conn *websocket.Conn) {
	// Do not push a mode-specific stream until the client identifies itself.
	// This avoids leaking regular game state to a connection that will
	// immediately authenticate into simulation mode.
}

func (a *App) handleDisconnect(conn *websocket.Conn) {
	a.mu.Lock()
	delete(a.connSimulation, conn)
	delete(a.connIdentified, conn)
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
		a.sendError(conn, "subscribe_wallet is no longer supported; use signed signin")
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
	if abs64(time.Now().Unix()-msg.Timestamp) > bet.SignInExpiryWindow {
		a.sendError(conn, "invalid signin signature")
		a.hub.Unregister(conn)
		return
	}

	a.identifyConnection(conn, wallet, msg.Simulation)

	if msg.Simulation {
		a.handleSimSignIn(conn, wallet)
		return
	}

	// Regular-mode sign-in.
	ack := bet.SignInAck{
		Type:             "signin_ack",
		Wallet:           bet.WalletHex(wallet),
		Balance:          bet.CurrentBalance(wallet),
		Simulation:       false,
		NextNonce:        a.bets.NextNonce(wallet),
		MinimumBetAmount: bet.MinimumBetAmount,
		GameRadii:        a.currentGameRadii(),
		BetHistory:       a.bets.BetHistory(wallet, time.Now().AddDate(0, 0, -7)),
	}
	_ = a.hub.SendTo(conn, ack)
	a.sendGameState(conn)
}

func (a *App) handleSimSignIn(conn *websocket.Conn, wallet [20]byte) {
	// Initialize sim wallet with $100 if new.
	balance := a.sim.InitWallet(wallet)
	a.sim.SyncLiveGames(a.filteredGames())

	ack := bet.SignInAck{
		Type:             "signin_ack",
		Wallet:           bet.WalletHex(wallet),
		Balance:          balance,
		Simulation:       true,
		NextNonce:        a.sim.NextNonce(wallet),
		MinimumBetAmount: bet.MinimumBetAmount,
		GameRadii:        a.sim.GameRadii(),
		BetHistory:       a.sim.BetHistory(wallet, time.Now().AddDate(0, 0, -7)),
	}
	_ = a.hub.SendTo(conn, ack)

	// Send the current isolated simulation game views to this connection.
	if games := a.sim.Games(); len(games) > 0 {
		_ = a.hub.SendTo(conn, ws.GameStateMessage{
			Type:            "game_state",
			Games:           games,
			Simulation:      true,
			ContractVersion: game.ContractVersionAssumedPossessionV1,
		})
	}
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
	identifiedWallet, hasWallet := a.connWallets[conn]
	connSim := a.connSimulation[conn]
	identified := a.connIdentified[conn]
	a.mu.RUnlock()
	if !identified {
		a.sendError(conn, "signin required before place_bet")
		return
	}
	if hasWallet && identifiedWallet != wallet {
		a.sendError(conn, "wallet does not match identified connection")
		return
	}

	if connSim != msg.Simulation {
		a.sendError(conn, "simulation flag does not match identified connection mode")
		return
	}

	// Route to simulation or regular engine based on the identified connection mode.
	if connSim {
		a.placeSimBet(conn, wallet, msg, signature)
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
		Simulation:        false,
		MinimumMultiplier: msg.MinimumMultiplier,
		Signature:         signature,
	}
	state, ok := a.manager.Game(msg.GameID)
	if !ok {
		ack := a.bets.RejectBet(b, "unknown gameId", &game.BetContractResolution{
			ContractVersion: game.ContractVersionAssumedPossessionV1,
			Kind:            game.ResolutionRejectedMarketClosed,
			Reasoning:       "backend has no live game state for the requested game",
		})
		a.identifyConnection(conn, wallet, false)
		_ = a.hub.SendTo(conn, ack)
		return
	}
	decision := a.tracker.Admit(state, msg.RoundID, msg.Timestamp, time.Duration(bet.BetExpiryWindow)*time.Second)
	if !decision.Accepted {
		ack := a.bets.RejectBet(b, decision.Reason, decision.Resolution)
		log.Printf("[assumed-possession] live_bet_rejected game=%s round=%s wallet=%s kind=%s reason=%q", msg.GameID, msg.RoundID, msg.Wallet, decision.Resolution.Kind, decision.Resolution.Reasoning)
		a.identifyConnection(conn, wallet, false)
		_ = a.hub.SendTo(conn, ack)
		return
	}
	b.ContractBinding = decision.Binding
	ack, err := a.bets.PlaceBet(b)
	if err != nil {
		a.sendError(conn, err.Error())
		return
	}
	a.identifyConnection(conn, wallet, false)
	if ack != nil && ack.Status == "accepted" && decision.Binding != nil {
		log.Printf("[assumed-possession] live_bet_accepted game=%s round=%s wallet=%s assumed_team=%s confidence=%s", msg.GameID, msg.RoundID, msg.Wallet, decision.Binding.AssumedTeam, decision.Binding.Confidence)
	}
	_ = a.hub.SendTo(conn, ack)
}

func (a *App) placeSimBet(conn *websocket.Conn, wallet [20]byte, msg ws.PlaceBetMessage, signature []byte) {
	b := &bet.Bet{
		Wallet:            wallet,
		GameID:            msg.GameID,
		RoundID:           msg.RoundID,
		Coordinate:        game.Coord{X: msg.X, Y: msg.Y},
		Amount:            msg.Amount,
		Nonce:             msg.Nonce,
		Timestamp:         msg.Timestamp,
		BetRadius:         msg.BetRadius,
		Simulation:        true,
		MinimumMultiplier: msg.MinimumMultiplier,
		Signature:         signature,
	}
	state, ok := a.sim.Game(msg.GameID)
	if !ok {
		ack := a.sim.Engine.RejectBet(b, "unknown gameId", &game.BetContractResolution{
			ContractVersion: game.ContractVersionAssumedPossessionV1,
			Kind:            game.ResolutionRejectedMarketClosed,
			Reasoning:       "backend has no isolated simulation game state for the requested game",
		})
		a.identifyConnection(conn, wallet, true)
		_ = a.hub.SendTo(conn, ack)
		return
	}
	decision, ok := a.sim.Admit(msg.GameID, msg.RoundID, msg.Timestamp, time.Duration(bet.BetExpiryWindow)*time.Second)
	if !ok {
		ack := a.sim.Engine.RejectBet(b, "unknown gameId", &game.BetContractResolution{
			ContractVersion: game.ContractVersionAssumedPossessionV1,
			Kind:            game.ResolutionRejectedMarketClosed,
			Reasoning:       "backend has no isolated simulation game state for the requested game",
		})
		a.identifyConnection(conn, wallet, true)
		_ = a.hub.SendTo(conn, ack)
		return
	}
	if !decision.Accepted {
		ack := a.sim.Engine.RejectBet(b, decision.Reason, decision.Resolution)
		laneID := ""
		if state.AssumedPossession != nil {
			laneID = state.AssumedPossession.Lane.LaneID
		}
		log.Printf("[assumed-possession] sim_bet_rejected game=%s round=%s wallet=%s kind=%s reason=%q lane=%s", msg.GameID, msg.RoundID, msg.Wallet, decision.Resolution.Kind, decision.Resolution.Reasoning, laneID)
		a.identifyConnection(conn, wallet, true)
		_ = a.hub.SendTo(conn, ack)
		return
	}
	b.ContractBinding = decision.Binding
	ack, err := a.sim.Engine.PlaceBet(b)
	if err != nil {
		a.sendError(conn, err.Error())
		return
	}
	a.identifyConnection(conn, wallet, true)
	if ack != nil && ack.Status == "accepted" && decision.Binding != nil {
		log.Printf("[assumed-possession] sim_bet_accepted game=%s round=%s wallet=%s assumed_team=%s confidence=%s", msg.GameID, msg.RoundID, msg.Wallet, decision.Binding.AssumedTeam, decision.Binding.Confidence)
	}
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
	a.connIdentified[conn] = true
	if a.walletConns[wallet] == nil {
		a.walletConns[wallet] = make(map[*websocket.Conn]struct{})
	}
	a.walletConns[wallet][conn] = struct{}{}
}

// --- regular-mode event handling ---

func (a *App) handlePlayEvent(event game.PlayEvent) {
	a.tracker.OnPlay(event)
	if state, ok := a.manager.Game(event.GameID); ok {
		a.sim.ObserveLiveEvent(state, event)
		if snapshot := a.tracker.Snapshot(state); snapshot != nil {
			log.Printf("[assumed-possession] market_update game=%s round=%s state=%s assumed_team=%s confidence=%s reason=%q lane=%s", state.GameID, snapshot.BoundRoundID, snapshot.MarketState, snapshot.AssumedTeam, snapshot.Confidence, snapshot.Reasoning, snapshot.Lane.LaneID)
		}
	}
	a.bets.OnPlayEvent(event)
	// Broadcast only to regular-mode connections.
	a.hub.BroadcastFiltered(ws.PlayEventMessage{
		Type:       "play_event",
		GameID:     event.GameID,
		PlayID:     event.PlayID,
		Sport:      event.Sport,
		Timestamp:  event.Timestamp.UTC().Format(time.RFC3339),
		Location:   event.Location,
		Event:      event.EventData,
		Simulation: false,
	}, a.isRegularConn)
}

// --- simulation-mode event handling ---

func (a *App) handleSimPlayEvent(event game.PlayEvent) {
	// Broadcast only to simulation-mode connections.
	a.hub.BroadcastFiltered(ws.PlayEventMessage{
		Type:       "play_event",
		GameID:     event.GameID,
		PlayID:     event.PlayID,
		Sport:      event.Sport,
		Timestamp:  event.Timestamp.UTC().Format(time.RFC3339),
		Location:   event.Location,
		Event:      event.EventData,
		Simulation: true,
	}, a.isSimConn)
}

// --- bet result forwarding (regular) ---

func (a *App) forwardBetResults(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-a.bets.ResultChan():
			a.sendToWalletMode(item.Wallet, item.Result, false)
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
			a.sendToWalletMode(wallet, update, false)
		}
	}
}

// --- bet result forwarding (simulation) ---

func (a *App) forwardSimBetResults(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-a.sim.Engine.ResultChan():
			a.sendToWalletMode(item.Wallet, item.Result, true)
		}
	}
}

func (a *App) forwardSimBalanceUpdates(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case update := <-a.sim.Engine.BalanceChan():
			wallet, err := bet.ParseWallet(update.Wallet)
			if err != nil {
				continue
			}
			a.sendToWalletMode(wallet, update, true)
		}
	}
}

// --- game state broadcasting ---

func (a *App) broadcastGameStates(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	a.sim.SyncLiveGames(a.filteredGames())
	a.hub.BroadcastFiltered(a.currentGameStateMessage(), a.isRegularConn)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.sim.SyncLiveGames(a.filteredGames())
			a.hub.BroadcastFiltered(a.currentGameStateMessage(), a.isRegularConn)
		}
	}
}

func (a *App) broadcastSimGameStates(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			games := a.sim.Games()
			if len(games) == 0 {
				continue
			}
			a.hub.BroadcastFiltered(ws.GameStateMessage{
				Type:            "game_state",
				Games:           games,
				Simulation:      true,
				ContractVersion: game.ContractVersionAssumedPossessionV1,
			}, a.isSimConn)
		}
	}
}

func (a *App) currentGameStateMessage() ws.GameStateMessage {
	games := a.filteredGames()
	for i := range games {
		if snapshot := a.tracker.Snapshot(games[i]); snapshot != nil {
			games[i].ContractVersion = game.ContractVersionAssumedPossessionV1
			games[i].AssumedPossession = snapshot
		}
	}
	sort.SliceStable(games, func(i, j int) bool {
		if games[i].Sport == games[j].Sport {
			return games[i].GameID < games[j].GameID
		}
		return games[i].Sport < games[j].Sport
	})
	return ws.GameStateMessage{Type: "game_state", Games: games, Simulation: false, ContractVersion: game.ContractVersionAssumedPossessionV1}
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

// --- mode-aware connection helpers ---

func (a *App) isSimConn(conn *websocket.Conn) bool {
	a.mu.RLock()
	identified := a.connIdentified[conn]
	v := a.connSimulation[conn]
	a.mu.RUnlock()
	return identified && v
}

func (a *App) isRegularConn(conn *websocket.Conn) bool {
	a.mu.RLock()
	identified := a.connIdentified[conn]
	v := a.connSimulation[conn]
	a.mu.RUnlock()
	return identified && !v
}

// sendToWalletMode sends a message to all connections for a wallet that match
// the given simulation mode, ensuring no cross-mode leakage.
func (a *App) sendToWalletMode(wallet [20]byte, msg any, simulation bool) {
	a.mu.RLock()
	conns := a.walletConns[wallet]
	snapshot := make([]*websocket.Conn, 0, len(conns))
	for conn := range conns {
		if a.connSimulation[conn] == simulation {
			snapshot = append(snapshot, conn)
		}
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
