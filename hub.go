package main

import (
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
	clients map[*websocket.Conn]*sync.Mutex
}

func NewHub() *Hub {
	return &Hub{clients: make(map[*websocket.Conn]*sync.Mutex)}
}

func (h *Hub) Register(conn *websocket.Conn) {
	h.mu.Lock()
	h.clients[conn] = &sync.Mutex{}
	h.mu.Unlock()
}

func (h *Hub) Unregister(conn *websocket.Conn) {
	h.mu.Lock()
	delete(h.clients, conn)
	h.mu.Unlock()
	_ = conn.Close()
}

func (h *Hub) Broadcast(msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[ws] marshal error: %v", err)
		return
	}
	h.mu.RLock()
	snapshot := make(map[*websocket.Conn]*sync.Mutex, len(h.clients))
	for conn, mu := range h.clients {
		snapshot[conn] = mu
	}
	h.mu.RUnlock()
	for conn, mu := range snapshot {
		mu.Lock()
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		err := conn.WriteMessage(websocket.TextMessage, data)
		mu.Unlock()
		if err != nil {
			h.Unregister(conn)
		}
	}
}

func (h *Hub) SendTo(conn *websocket.Conn, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	h.mu.RLock()
	mu, ok := h.clients[conn]
	h.mu.RUnlock()
	if !ok {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	conn.SetWriteDeadline(time.Now().Add(writeWait))
	return conn.WriteMessage(websocket.TextMessage, data)
}

func (h *Hub) HandleWS(onConnect func(*websocket.Conn), onDisconnect func(*websocket.Conn), onMessage func(*websocket.Conn, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[ws] upgrade failed: %v", err)
			return
		}
		h.Register(conn)
		if onConnect != nil {
			onConnect(conn)
		}
		conn.SetReadDeadline(time.Now().Add(pongWait))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(pongWait))
			return nil
		})
		pingDone := make(chan struct{})
		go func() {
			ticker := time.NewTicker(pingInterval)
			defer ticker.Stop()
			for {
				select {
				case <-pingDone:
					return
				case <-ticker.C:
					h.mu.RLock()
					mu, ok := h.clients[conn]
					h.mu.RUnlock()
					if !ok {
						return
					}
					mu.Lock()
					conn.SetWriteDeadline(time.Now().Add(writeWait))
					err := conn.WriteMessage(websocket.PingMessage, nil)
					mu.Unlock()
					if err != nil {
						return
					}
				}
			}
		}()
		go func() {
			defer func() {
				close(pingDone)
				if onDisconnect != nil {
					onDisconnect(conn)
				}
				h.Unregister(conn)
			}()
			for {
				msgType, payload, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if msgType == websocket.TextMessage && onMessage != nil {
					onMessage(conn, payload)
				}
			}
		}()
	}
}
