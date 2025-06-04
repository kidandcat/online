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
	Subdomain string
	conn      *websocket.Conn
	requests  chan *TunnelRequest
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
		for subdomain, tunnel := range tm.tunnels {
			if time.Since(tunnel.created) > 24*time.Hour {
				tunnel.Close()
				delete(tm.tunnels, subdomain)
				log.Printf("Cleaned up expired tunnel: %s", subdomain)
			}
		}
		tm.mu.Unlock()
	}
}

func (tm *TunnelManager) CreateTunnel(subdomain string, conn *websocket.Conn) (*Tunnel, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	if subdomain == "" {
		subdomain = generateSubdomain()
	}
	
	if _, exists := tm.tunnels[subdomain]; exists {
		return nil, fmt.Errorf("subdomain %s already in use", subdomain)
	}
	
	tunnel := &Tunnel{
		ID:        uuid.New().String(),
		Subdomain: subdomain,
		conn:      conn,
		requests:  make(chan *TunnelRequest, 100),
		created:   time.Now(),
	}
	
	tm.tunnels[subdomain] = tunnel
	
	// Start handling tunnel messages
	go tunnel.handleMessages()
	
	return tunnel, nil
}

func (tm *TunnelManager) GetTunnel(subdomain string) (*Tunnel, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	tunnel, exists := tm.tunnels[subdomain]
	return tunnel, exists
}

func (tm *TunnelManager) RemoveTunnel(subdomain string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	if tunnel, exists := tm.tunnels[subdomain]; exists {
		tunnel.Close()
		delete(tm.tunnels, subdomain)
	}
}

func (t *Tunnel) handleMessages() {
	defer func() {
		close(t.requests)
	}()
	
	for {
		var resp TunnelResponse
		err := t.conn.ReadJSON(&resp)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Tunnel %s disconnected: %v", t.Subdomain, err)
			}
			break
		}
		
		// Handle response (this would be matched with pending requests)
		log.Printf("Received response for request %s", resp.ID)
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
	case <-time.After(30 * time.Second):
		http.Error(w, "Request timeout", http.StatusGatewayTimeout)
		return
	}
}

func (t *Tunnel) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	if t.conn != nil {
		t.conn.Close()
	}
}

func generateSubdomain() string {
	return uuid.New().String()[:8]
}