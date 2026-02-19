package bridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// sseClient represents a single SSE subscriber.
type sseClient struct {
	ch chan []byte
}

// SSEHub manages Server-Sent Event connections.
// It fans out events to all connected browser clients.
type SSEHub struct {
	mu      sync.RWMutex
	clients map[*sseClient]struct{}
}

// NewSSEHub creates a new SSE hub.
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[*sseClient]struct{}),
	}
}

// register adds a new client.
func (h *SSEHub) register() *sseClient {
	c := &sseClient{ch: make(chan []byte, 256)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

// unregister removes a client.
func (h *SSEHub) unregister(c *sseClient) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// ClientCount returns the number of connected SSE clients.
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Broadcast sends an event to all connected clients.
// Non-blocking: if a client's buffer is full, the message is dropped for that client.
func (h *SSEHub) Broadcast(eventType string, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	msg := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, payload))

	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.ch <- msg:
		default:
			// drop — client can't keep up
		}
	}
}

// ServeHTTP implements the SSE endpoint handler.
func (h *SSEHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	c := h.register()
	defer h.unregister(c)

	// Initial ping so the client knows the connection is alive.
	fmt.Fprint(w, "event: ping\ndata: {}\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-c.ch:
			w.Write(msg)
			flusher.Flush()
		}
	}
}
