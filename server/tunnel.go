package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type TunnelManager struct {
	tunnels map[string]*Tunnel
	mu      sync.RWMutex
}

type Tunnel struct {
	ID        string
	Path      string
	conn      *websocket.Conn
	requests  chan *TunnelRequest
	responses map[string]chan *TunnelResponse
	created   time.Time
	mu        sync.Mutex
}

type TunnelRequest struct {
	ID      string
	Method  string
	Path    string
	Headers map[string][]string
	Body    []byte
}

type TunnelResponse struct {
	ID         string              `json:"id"`
	StatusCode int                 `json:"statusCode"`
	Headers    map[string][]string `json:"headers"`
	Body       []byte              `json:"body"`
}

var Upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func NewTunnelManager() *TunnelManager {
	tm := &TunnelManager{
		tunnels: make(map[string]*Tunnel),
	}
	
	// Clean up expired tunnels periodically
	go tm.cleanupExpiredTunnels()
	
	return tm
}

func (tm *TunnelManager) cleanupExpiredTunnels() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		tm.mu.Lock()
		for path, tunnel := range tm.tunnels {
			if time.Since(tunnel.created) > 24*time.Hour {
				tunnel.Close()
				delete(tm.tunnels, path)
				log.Printf("Cleaned up expired tunnel: %s", path)
			}
		}
		tm.mu.Unlock()
	}
}

func (tm *TunnelManager) CreateTunnel(conn *websocket.Conn) (*Tunnel, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	// Generate a unique path identifier
	path := generateTunnelPath()
	
	if _, exists := tm.tunnels[path]; exists {
		return nil, fmt.Errorf("tunnel path %s already in use", path)
	}
	
	tunnel := &Tunnel{
		ID:        uuid.New().String(),
		Path:      path,
		conn:      conn,
		requests:  make(chan *TunnelRequest, 100),
		responses: make(map[string]chan *TunnelResponse),
		created:   time.Now(),
	}
	
	tm.tunnels[path] = tunnel
	
	// Start handling tunnel messages
	go tunnel.handleMessages()
	
	return tunnel, nil
}

func (tm *TunnelManager) GetTunnel(path string) (*Tunnel, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	tunnel, exists := tm.tunnels[path]
	return tunnel, exists
}

func (tm *TunnelManager) RemoveTunnel(path string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	if tunnel, exists := tm.tunnels[path]; exists {
		tunnel.Close()
		delete(tm.tunnels, path)
	}
}

func (t *Tunnel) handleMessages() {
	defer func() {
		close(t.requests)
		// Clean up response channels
		t.mu.Lock()
		for _, ch := range t.responses {
			close(ch)
		}
		t.mu.Unlock()
	}()
	
	for {
		var resp TunnelResponse
		err := t.conn.ReadJSON(&resp)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Tunnel %s disconnected: %v", t.Path, err)
			}
			break
		}
		
		// Find the response channel for this request
		t.mu.Lock()
		ch, exists := t.responses[resp.ID]
		if exists {
			delete(t.responses, resp.ID)
		}
		t.mu.Unlock()
		
		if exists {
			// Send response to waiting handler
			select {
			case ch <- &resp:
				log.Printf("Delivered response for request %s", resp.ID)
			default:
				log.Printf("Failed to deliver response for request %s (channel blocked)", resp.ID)
			}
			close(ch)
		} else {
			log.Printf("Received response for unknown request %s", resp.ID)
		}
	}
}

func (t *Tunnel) ForwardRequest(w http.ResponseWriter, r *http.Request) {
	reqID := uuid.New().String()
	
	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadGateway)
		return
	}
	
	// Create response channel
	respChan := make(chan *TunnelResponse, 1)
	t.mu.Lock()
	t.responses[reqID] = respChan
	t.mu.Unlock()
	
	// Clean up channel on exit
	defer func() {
		t.mu.Lock()
		delete(t.responses, reqID)
		t.mu.Unlock()
	}()
	
	// Create tunnel request
	tunnelReq := &TunnelRequest{
		ID:      reqID,
		Method:  r.Method,
		Path:    r.URL.Path,
		Headers: r.Header,
		Body:    body,
	}
	
	// Send request to client through WebSocket
	t.mu.Lock()
	err = t.conn.WriteJSON(tunnelReq)
	t.mu.Unlock()
	
	if err != nil {
		http.Error(w, "Failed to forward request", http.StatusBadGateway)
		return
	}
	
	// Wait for response with timeout
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	
	select {
	case <-ctx.Done():
		http.Error(w, "Request timeout", http.StatusGatewayTimeout)
		return
	case resp := <-respChan:
		if resp == nil {
			http.Error(w, "Connection closed", http.StatusBadGateway)
			return
		}
		
		// Write response headers
		for k, v := range resp.Headers {
			for _, vv := range v {
				w.Header().Add(k, vv)
			}
		}
		
		// Write status code
		w.WriteHeader(resp.StatusCode)
		
		// Write response body
		if _, err := w.Write(resp.Body); err != nil {
			log.Printf("Failed to write response body: %v", err)
		}
	}
}

func (t *Tunnel) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	if t.conn != nil {
		t.conn.Close()
	}
}

func generateTunnelPath() string {
	return "tunnel/" + uuid.New().String()[:8]
}