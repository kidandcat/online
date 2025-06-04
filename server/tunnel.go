package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type TunnelManager struct {
	activeTunnel *Tunnel
	mu           sync.RWMutex
}

type Tunnel struct {
	ID        string
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
	tm := &TunnelManager{}
	
	// Clean up expired tunnel periodically
	go tm.cleanupExpiredTunnel()
	
	return tm
}

func (tm *TunnelManager) cleanupExpiredTunnel() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		tm.mu.Lock()
		if tm.activeTunnel != nil && time.Since(tm.activeTunnel.created) > 24*time.Hour {
			tm.activeTunnel.Close()
			tm.activeTunnel = nil
			log.Printf("Cleaned up expired tunnel")
		}
		tm.mu.Unlock()
	}
}

func (tm *TunnelManager) CreateTunnel(conn *websocket.Conn) (*Tunnel, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	// Only allow one active tunnel
	if tm.activeTunnel != nil {
		return nil, fmt.Errorf("a tunnel is already active")
	}
	
	tunnel := &Tunnel{
		ID:        uuid.New().String(),
		conn:      conn,
		requests:  make(chan *TunnelRequest, 100),
		responses: make(map[string]chan *TunnelResponse),
		created:   time.Now(),
	}
	
	tm.activeTunnel = tunnel
	
	// Start handling tunnel messages
	go tunnel.handleMessages()
	
	return tunnel, nil
}

func (tm *TunnelManager) GetActiveTunnel() (*Tunnel, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	if tm.activeTunnel != nil {
		return tm.activeTunnel, true
	}
	return nil, false
}


func (tm *TunnelManager) RemoveTunnel() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	if tm.activeTunnel != nil {
		tm.activeTunnel.Close()
		tm.activeTunnel = nil
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
				log.Printf("Tunnel disconnected: %v", err)
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
			// Special handling for Content-Type to ensure proper MIME types
			if strings.ToLower(k) == "content-type" {
				// Check if we need to correct the MIME type based on the path
				if correctedType := getCorrectContentType(r.URL.Path, v); correctedType != "" {
					w.Header().Set("Content-Type", correctedType)
					continue
				}
			}
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

// getCorrectContentType checks if the Content-Type needs correction based on file extension
func getCorrectContentType(path string, currentTypes []string) string {
	// Get the file extension
	ext := strings.ToLower(filepath.Ext(path))
	
	// Map of extensions to correct MIME types
	correctTypes := map[string]string{
		".css":  "text/css",
		".js":   "application/javascript",
		".json": "application/json",
		".html": "text/html",
		".htm":  "text/html",
		".xml":  "application/xml",
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".svg":  "image/svg+xml",
		".ico":  "image/x-icon",
		".woff": "font/woff",
		".woff2": "font/woff2",
		".ttf":  "font/ttf",
		".otf":  "font/otf",
	}
	
	// Check if we have a known extension
	if correctType, ok := correctTypes[ext]; ok {
		// Check if the current type is incorrect
		if len(currentTypes) > 0 {
			currentType := strings.ToLower(currentTypes[0])
			// If it's text/plain or application/octet-stream for a known type, correct it
			if currentType == "text/plain" || currentType == "application/octet-stream" {
				return correctType
			}
		}
	}
	
	return ""
}

