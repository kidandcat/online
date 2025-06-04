package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestNewTunnelManager(t *testing.T) {
	tm := NewTunnelManager()
	if tm == nil {
		t.Fatal("Expected TunnelManager to be created")
	}
	if tm.activeTunnel != nil {
		t.Error("Expected no active tunnel initially")
	}
}

func TestCreateTunnel(t *testing.T) {
	tm := NewTunnelManager()
	
	// Create a test WebSocket connection
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Failed to upgrade connection: %v", err)
		}
		defer conn.Close()
		
		// Keep connection open
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()
	
	// Connect to test server
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()
	
	// Create tunnel
	tunnel, err := tm.CreateTunnel(conn)
	if err != nil {
		t.Fatalf("Failed to create tunnel: %v", err)
	}
	
	if tunnel.ID == "" {
		t.Error("Expected tunnel to have an ID")
	}
	
	// Try to create another tunnel (should fail)
	_, err = tm.CreateTunnel(conn)
	if err == nil {
		t.Error("Expected error when creating second tunnel")
	}
	if !strings.Contains(err.Error(), "already active") {
		t.Errorf("Expected 'already active' error, got: %v", err)
	}
}

func TestGetActiveTunnel(t *testing.T) {
	tm := NewTunnelManager()
	
	// No active tunnel initially
	tunnel, exists := tm.GetActiveTunnel()
	if exists {
		t.Error("Expected no active tunnel initially")
	}
	if tunnel != nil {
		t.Error("Expected nil tunnel when none exists")
	}
	
	// Create a tunnel
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := Upgrader.Upgrade(w, r, nil)
		defer conn.Close()
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()
	
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
	defer conn.Close()
	
	createdTunnel, _ := tm.CreateTunnel(conn)
	
	// Should get the active tunnel
	tunnel, exists = tm.GetActiveTunnel()
	if !exists {
		t.Error("Expected active tunnel to exist")
	}
	if tunnel.ID != createdTunnel.ID {
		t.Error("Expected to get the same tunnel")
	}
}

func TestRemoveTunnel(t *testing.T) {
	tm := NewTunnelManager()
	
	// Create a tunnel
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := Upgrader.Upgrade(w, r, nil)
		defer conn.Close()
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()
	
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
	defer conn.Close()
	
	tm.CreateTunnel(conn)
	
	// Remove the tunnel
	tm.RemoveTunnel()
	
	// Should have no active tunnel
	_, exists := tm.GetActiveTunnel()
	if exists {
		t.Error("Expected no active tunnel after removal")
	}
}

func TestTunnelForwardRequest(t *testing.T) {
	// Create channels for coordination
	requestReceived := make(chan *TunnelRequest)
	responseSent := make(chan bool)
	
	// Create WebSocket server that acts as the client
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Failed to upgrade: %v", err)
		}
		defer conn.Close()
		
		// Read tunnel request
		var req TunnelRequest
		if err := conn.ReadJSON(&req); err != nil {
			t.Fatalf("Failed to read request: %v", err)
		}
		
		requestReceived <- &req
		
		// Send response
		resp := TunnelResponse{
			ID:         req.ID,
			StatusCode: http.StatusOK,
			Headers: map[string][]string{
				"Content-Type": {"application/json"},
			},
			Body: []byte(`{"status":"ok"}`),
		}
		
		if err := conn.WriteJSON(resp); err != nil {
			t.Fatalf("Failed to write response: %v", err)
		}
		
		responseSent <- true
		
		// Keep connection open
		<-time.After(100 * time.Millisecond)
	}))
	defer server.Close()
	
	// Connect to WebSocket server
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()
	
	// Create tunnel
	tm := NewTunnelManager()
	tunnel, err := tm.CreateTunnel(conn)
	if err != nil {
		t.Fatalf("Failed to create tunnel: %v", err)
	}
	
	// Make HTTP request through tunnel
	req := httptest.NewRequest("POST", "/test-path", strings.NewReader(`{"test":"data"}`))
	req.Header.Set("Content-Type", "application/json")
	
	recorder := httptest.NewRecorder()
	
	// Forward request in goroutine
	done := make(chan bool)
	go func() {
		tunnel.ForwardRequest(recorder, req)
		done <- true
	}()
	
	// Wait for request to be received
	receivedReq := <-requestReceived
	if receivedReq.Method != "POST" {
		t.Errorf("Expected method POST, got %s", receivedReq.Method)
	}
	if receivedReq.Path != "/test-path" {
		t.Errorf("Expected path /test-path, got %s", receivedReq.Path)
	}
	if string(receivedReq.Body) != `{"test":"data"}` {
		t.Errorf("Expected body {\"test\":\"data\"}, got %s", string(receivedReq.Body))
	}
	
	// Wait for response to be sent
	<-responseSent
	
	// Wait for forward to complete
	<-done
	
	// Check response
	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}
	if recorder.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", recorder.Header().Get("Content-Type"))
	}
	if recorder.Body.String() != `{"status":"ok"}` {
		t.Errorf("Expected body {\"status\":\"ok\"}, got %s", recorder.Body.String())
	}
}

func TestTunnelTimeout(t *testing.T) {
	// Create WebSocket server that doesn't respond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		
		// Read request but don't respond
		var req TunnelRequest
		conn.ReadJSON(&req)
		
		// Keep connection open without responding
		time.Sleep(2 * time.Second)
	}))
	defer server.Close()
	
	// Connect to WebSocket server
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
	defer conn.Close()
	
	// Create tunnel
	tm := NewTunnelManager()
	tunnel, _ := tm.CreateTunnel(conn)
	
	// Make request with short timeout
	req := httptest.NewRequest("GET", "/timeout-test", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 100*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	
	recorder := httptest.NewRecorder()
	
	// Start timing
	start := time.Now()
	tunnel.ForwardRequest(recorder, req)
	duration := time.Since(start)
	
	// Should timeout quickly
	if duration > 200*time.Millisecond {
		t.Errorf("Expected quick timeout, took %v", duration)
	}
	
	// Should return timeout error
	if recorder.Code != http.StatusGatewayTimeout {
		t.Errorf("Expected status 504, got %d", recorder.Code)
	}
}

func TestGetCorrectContentType(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		currentTypes []string
		expected     string
	}{
		{
			name:         "CSS file with text/plain",
			path:         "/style.css",
			currentTypes: []string{"text/plain"},
			expected:     "text/css",
		},
		{
			name:         "JS file with application/octet-stream",
			path:         "/script.js",
			currentTypes: []string{"application/octet-stream"},
			expected:     "application/javascript",
		},
		{
			name:         "CSS file with correct type",
			path:         "/style.css",
			currentTypes: []string{"text/css"},
			expected:     "",
		},
		{
			name:         "Unknown extension",
			path:         "/file.xyz",
			currentTypes: []string{"text/plain"},
			expected:     "",
		},
		{
			name:         "No extension",
			path:         "/file",
			currentTypes: []string{"text/plain"},
			expected:     "",
		},
		{
			name:         "SVG file",
			path:         "/image.svg",
			currentTypes: []string{"text/plain"},
			expected:     "image/svg+xml",
		},
		{
			name:         "Font file WOFF2",
			path:         "/font.woff2",
			currentTypes: []string{"application/octet-stream"},
			expected:     "font/woff2",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getCorrectContentType(tt.path, tt.currentTypes)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestConcurrentResponses(t *testing.T) {
	// Create a tunnel with mock connection
	tunnel := &Tunnel{
		ID:        "test-tunnel",
		responses: make(map[string]chan *TunnelResponse),
	}
	
	// Test concurrent access to responses map
	var wg sync.WaitGroup
	errors := make(chan error, 100)
	
	// Spawn multiple goroutines to access the map
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			// Add response channel
			reqID := fmt.Sprintf("req-%d", id)
			respChan := make(chan *TunnelResponse, 1)
			
			tunnel.mu.Lock()
			tunnel.responses[reqID] = respChan
			tunnel.mu.Unlock()
			
			// Simulate some work
			time.Sleep(10 * time.Millisecond)
			
			// Remove response channel
			tunnel.mu.Lock()
			delete(tunnel.responses, reqID)
			tunnel.mu.Unlock()
		}(i)
	}
	
	// Wait for all goroutines
	wg.Wait()
	close(errors)
	
	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent access error: %v", err)
	}
	
	// Responses map should be empty
	if len(tunnel.responses) != 0 {
		t.Errorf("Expected empty responses map, got %d entries", len(tunnel.responses))
	}
}

func TestCleanupExpiredTunnel(t *testing.T) {
	tm := &TunnelManager{}
	
	// Create a mock tunnel with old timestamp
	oldTunnel := &Tunnel{
		ID:      "old-tunnel",
		created: time.Now().Add(-25 * time.Hour), // More than 24 hours old
	}
	
	// Mock connection
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := Upgrader.Upgrade(w, r, nil)
		defer conn.Close()
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()
	
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
	defer conn.Close()
	
	oldTunnel.conn = conn
	tm.activeTunnel = oldTunnel
	
	// Manually trigger cleanup
	tm.mu.Lock()
	if tm.activeTunnel != nil && time.Since(tm.activeTunnel.created) > 24*time.Hour {
		tm.activeTunnel.Close()
		tm.activeTunnel = nil
	}
	tm.mu.Unlock()
	
	// Check that tunnel was removed
	if tm.activeTunnel != nil {
		t.Error("Expected tunnel to be cleaned up")
	}
}