package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ChatMessage represents a single chat message.
type ChatMessage struct {
	Name string `json:"name"`
	Text string `json:"text"`
	Time string `json:"time"`
}

// chatClient is an SSE subscriber — it has a channel that receives
// new messages as they arrive. When the HTTP handler returns (client
// disconnects), the channel is closed and the client is removed.
type chatClient struct {
	ch     chan ChatMessage
	done   chan struct{}
	closed bool
}

// ChatHub manages the message ring buffer and SSE fan-out.
type ChatHub struct {
	mu       sync.Mutex
	messages []ChatMessage // ring buffer, capped at maxMessages
	head     int           // write position
	count    int           // number of messages stored
	clients  map[*chatClient]struct{}
}

const maxChatMessages = 100

func newChatHub() *ChatHub {
	return &ChatHub{
		messages: make([]ChatMessage, maxChatMessages),
		clients:  make(map[*chatClient]struct{}),
	}
}

// post adds a message to the ring buffer and fans it out to all
// connected SSE clients.
func (h *ChatHub) post(msg ChatMessage) {
	h.mu.Lock()
	// Ring buffer write.
	h.messages[h.head] = msg
	h.head = (h.head + 1) % maxChatMessages
	if h.count < maxChatMessages {
		h.count++
	}
	// Snapshot clients so we can send without holding the lock.
	clients := make([]*chatClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		select {
		case c.ch <- msg:
		default:
			// Client is slow — skip this message rather than
			// blocking the broadcaster.
		}
	}
}

// recent returns the last n messages in chronological order.
func (h *ChatHub) recent(n int) []ChatMessage {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.count == 0 {
		return nil
	}
	if n > h.count {
		n = h.count
	}
	out := make([]ChatMessage, n)
	start := (h.head - h.count + maxChatMessages) % maxChatMessages
	for i := 0; i < n; i++ {
		idx := (start + i) % maxChatMessages
		out[i] = h.messages[idx]
	}
	return out
}

// subscribe registers a new SSE client and returns its channel.
func (h *ChatHub) subscribe() *chatClient {
	c := &chatClient{
		ch:   make(chan ChatMessage, 32),
		done: make(chan struct{}),
	}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

// unsubscribe removes an SSE client.
func (h *ChatHub) unsubscribe(c *chatClient) {
	h.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.done)
	}
	delete(h.clients, c)
	h.mu.Unlock()
}

// clear resets the ring buffer and disconnects all SSE clients.
// Called when a stream ends so the next stream gets a fresh chat room.
func (h *ChatHub) clear() {
	h.mu.Lock()
	// Disconnect all SSE clients — they will auto-reconnect and
	// receive an empty history.
	for c := range h.clients {
		if !c.closed {
			c.closed = true
			close(c.done)
		}
	}
	// Reset ring buffer.
	h.messages = make([]ChatMessage, maxChatMessages)
	h.head = 0
	h.count = 0
	h.clients = make(map[*chatClient]struct{})
	h.mu.Unlock()
}

// HandleChatPost handles POST /api/chat.
// Body: {"name": "...", "text": "..."}
func (s *Session) HandleChatPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg ChatMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if msg.Name == "" {
		msg.Name = "anon"
	}
	if msg.Text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	// Sanitise length.
	if len(msg.Name) > 32 {
		msg.Name = msg.Name[:32]
	}
	if len(msg.Text) > 500 {
		msg.Text = msg.Text[:500]
	}
	msg.Time = time.Now().Format("15:04:05")

	s.chatHub.post(msg)
	s.logger.Debug().
		Str("name", msg.Name).
		Str("text", msg.Text).
		Msg("chat: message posted")

	w.WriteHeader(http.StatusCreated)
}

// HandleChatEvents handles GET /api/chat/events.
// It streams new chat messages as an SSE (Server-Sent Events) stream.
func (s *Session) HandleChatEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Send recent history so new joiners see context.
	recent := s.chatHub.recent(30)
	for _, msg := range recent {
		data, _ := json.Marshal(msg)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	client := s.chatHub.subscribe()
	defer s.chatHub.unsubscribe(client)

	// Keepalive ticker — send a comment every 15s to prevent
	// proxies from closing the connection.
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case msg, ok := <-client.ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(msg)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-client.done:
			return
		}
	}
}
