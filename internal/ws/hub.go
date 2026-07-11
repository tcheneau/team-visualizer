package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return u.Host == r.Host
	},
}

// Message is a WebSocket broadcast message.
type Message struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Hub manages connected WebSocket clients and broadcasts messages.
type Hub struct {
	mu         sync.RWMutex
	clients    map[*Client]bool
	register   chan *Client
	unregister chan *Client
	broadcast  chan Message
}

// NewHub creates a new WebSocket hub.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		register:   make(chan *Client, 64),
		unregister: make(chan *Client, 64),
		broadcast:  make(chan Message, 256),
	}
}

// Run starts the hub's event loop. Call as a goroutine.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Printf("ws: client connected (%s), total: %d", client.Username, h.count())

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			log.Printf("ws: client disconnected (%s), total: %d", client.Username, h.count())

		case msg := <-h.broadcast:
			data, _ := json.Marshal(msg)
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- data:
				default:
					// Client buffer full, skip (will be cleaned up on next read error)
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *Hub) count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Broadcast sends a message to all connected clients.
func (h *Hub) Broadcast(msgType string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		log.Printf("ws: broadcast marshal error: %v", err)
		return
	}
	h.broadcast <- Message{Type: msgType, Data: raw}
}

// BroadcastRaw sends a pre-encoded message.
func (h *Hub) BroadcastRaw(msgType string, raw json.RawMessage) {
	h.broadcast <- Message{Type: msgType, Data: raw}
}

// ServeWS handles WebSocket upgrade requests.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, username, role string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws: upgrade error: %v", err)
		return
	}

	client := &Client{
		hub:      h,
		conn:     conn,
		send:     make(chan []byte, 64),
		Username: username,
		Role:     role,
	}

	h.register <- client

	// Send a welcome message with current client info
	welcome, _ := json.Marshal(Message{
		Type: "connected",
		Data: rawJSON(map[string]any{"username": username, "role": role}),
	})
	client.send <- welcome

	go client.writePump()
	client.readPump()
}

func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}