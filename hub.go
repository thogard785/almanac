package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		// Tighten the WS origin policy to the actual deployed frontend and local dev
		// instead of accepting arbitrary browser origins.
		return strings.HasPrefix(origin, "https://zeroday.trade") ||
			strings.HasPrefix(origin, "http://localhost") ||
			strings.HasPrefix(origin, "http://127.0.0.1")
	},
}

const (
	pongWait     = 60 * time.Second
	pingInterval = 30 * time.Second
	writeWait    = 10 * time.Second
)

type Hub struct {
	mu      sync.RWMutex
	clients map[*websocket.Conn]*sync.Mutex // conn → write mutex
}

type WSHooks struct {
	OnConnect    func(*websocket.Conn)
	OnDisconnect func(*websocket.Conn)
	OnMessage    func(*websocket.Conn, []byte)
}

func NewHub() *Hub {
	return &Hub{
		clients: make(map[*websocket.Conn]*sync.Mutex),
	}
}

func (h *Hub) Register(conn *websocket.Conn) {
	h.mu.Lock()
	h.clients[conn] = &sync.Mutex{}
	h.mu.Unlock()
	log.Printf("[ws] client connected, total: %d", h.Count())
}

func (h *Hub) Unregister(conn *websocket.Conn) {
	h.mu.Lock()
	delete(h.clients, conn)
	h.mu.Unlock()
	conn.Close()
	log.Printf("[ws] client disconnected, total: %d", h.Count())
}

func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Broadcast sends a JSON message to all connected clients.
func (h *Hub) Broadcast(msg interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[ws] broadcast marshal error: %v", err)
		return
	}
	h.mu.RLock()
	snapshot := make(map[*websocket.Conn]*sync.Mutex, len(h.clients))
	for c, wm := range h.clients {
		snapshot[c] = wm
	}
	h.mu.RUnlock()

	for conn, wm := range snapshot {
		wm.Lock()
		err := conn.WriteMessage(websocket.TextMessage, data)
		wm.Unlock()
		if err != nil {
			h.Unregister(conn)
		}
	}
}

// RunBroadcastLoop reads from the updates channel and broadcasts to all WS clients.
func (h *Hub) RunBroadcastLoop(ctx context.Context, updates <-chan interface{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-updates:
			if !ok {
				return
			}
			h.Broadcast(msg)
		}
	}
}

// SendTo sends a JSON message to a single client.
func (h *Hub) SendTo(conn *websocket.Conn, msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	h.mu.RLock()
	wm, ok := h.clients[conn]
	h.mu.RUnlock()
	if !ok {
		return nil
	}
	wm.Lock()
	err = conn.WriteMessage(websocket.TextMessage, data)
	wm.Unlock()
	return err
}

// HandleWS upgrades an HTTP connection to WebSocket and manages its lifecycle.
func (h *Hub) HandleWS(hooks WSHooks) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[ws] upgrade failed: %v", err)
			return
		}
		h.Register(conn)

		if hooks.OnConnect != nil {
			hooks.OnConnect(conn)
		}

		// Configure pong handler
		conn.SetReadDeadline(time.Now().Add(pongWait))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(pongWait))
			return nil
		})

		// Ping ticker goroutine
		pingDone := make(chan struct{})
		go func() {
			ticker := time.NewTicker(pingInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					h.mu.RLock()
					wm, ok := h.clients[conn]
					h.mu.RUnlock()
					if !ok {
						return
					}
					wm.Lock()
					conn.SetWriteDeadline(time.Now().Add(writeWait))
					err := conn.WriteMessage(websocket.PingMessage, nil)
					wm.Unlock()
					if err != nil {
						return
					}
				case <-pingDone:
					return
				}
			}
		}()

		// Read loop for v2 client messages.
		go func() {
			defer func() {
				close(pingDone)
				if hooks.OnDisconnect != nil {
					hooks.OnDisconnect(conn)
				}
				h.Unregister(conn)
			}()
			for {
				msgType, payload, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if msgType == websocket.TextMessage && hooks.OnMessage != nil {
					hooks.OnMessage(conn, payload)
				}
			}
		}()
	}
}
