package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestNewClient(t *testing.T) {
	client := NewClient("https://example.com")
	if client.serverURL != "https://example.com" {
		t.Errorf("Expected serverURL to be https://example.com, got %s", client.serverURL)
	}
}

func TestGetWebSocketURL(t *testing.T) {
	tests := []struct {
		name      string
		serverURL string
		expected  string
	}{
		{
			name:      "HTTPS to WSS",
			serverURL: "https://example.com",
			expected:  "wss://example.com/ws/tunnel",
		},
		{
			name:      "HTTP to WS",
			serverURL: "http://example.com",
			expected:  "ws://example.com/ws/tunnel",
		},
		{
			name:      "Default to WSS",
			serverURL: "//example.com",
			expected:  "wss://example.com/ws/tunnel",
		},
		{
			name:      "With path",
			serverURL: "https://example.com/path",
			expected:  "wss://example.com/ws/tunnel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.serverURL)
			got := client.getWebSocketURL()
			if got != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, got)
			}
		})
	}
}

func TestConcurrentWrites(t *testing.T) {
	// Create a test WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Failed to upgrade connection: %v", err)
		}
		defer conn.Close()

		// Send initial tunnel info
		info := TunnelInfo{
			ID:  "test-tunnel",
			URL: "https://test.example.com/test-tunnel",
		}
		if err := conn.WriteJSON(info); err != nil {
			t.Fatalf("Failed to write tunnel info: %v", err)
		}

		// Read messages from client
		for {
			var resp TunnelResponse
			if err := conn.ReadJSON(&resp); err != nil {
				break
			}
		}
	}))
	defer server.Close()

	// Convert HTTP URL to WebSocket URL
	u, _ := url.Parse(server.URL)
	u.Scheme = "ws"

	client := NewClient(u.String())
	
	// Connect to server
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.Dial(u.String()+"/ws/tunnel", nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	client.conn = conn
	defer client.Close()

	// Read tunnel info
	var info TunnelInfo
	if err := conn.ReadJSON(&info); err != nil {
		t.Fatalf("Failed to read tunnel info: %v", err)
	}

	// Test concurrent writes
	done := make(chan bool)
	errors := make(chan error, 10)

	// Spawn multiple goroutines to write concurrently
	for i := 0; i < 10; i++ {
		go func(id int) {
			resp := TunnelResponse{
				ID:         fmt.Sprintf("req-%d", id),
				StatusCode: 200,
				Headers:    map[string][]string{"Content-Type": {"text/plain"}},
				Body:       []byte(fmt.Sprintf("Response %d", id)),
			}

			// This should be safe with the mutex
			client.mu.Lock()
			err := client.conn.WriteJSON(resp)
			client.mu.Unlock()

			if err != nil {
				errors <- err
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Check for errors
	close(errors)
	for err := range errors {
		t.Errorf("Concurrent write error: %v", err)
	}
}

func TestHandleRequest(t *testing.T) {
	// Create a local test server
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request details
		if r.Method != "POST" {
			t.Errorf("Expected method POST, got %s", r.Method)
		}
		if r.URL.Path != "/test-path" {
			t.Errorf("Expected path /test-path, got %s", r.URL.Path)
		}

		// Check that SSL headers are removed
		if r.Header.Get("X-Forwarded-Proto") != "http" {
			t.Errorf("Expected X-Forwarded-Proto to be http, got %s", r.Header.Get("X-Forwarded-Proto"))
		}

		// Send response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer localServer.Close()

	// Extract port from test server URL
	u, _ := url.Parse(localServer.URL)
	port := u.Port()

	// Create WebSocket test server
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send tunnel info
		info := TunnelInfo{
			ID:  "test-tunnel",
			URL: "https://test.example.com/test-tunnel",
		}
		conn.WriteJSON(info)

		// Send test request
		req := TunnelRequest{
			ID:     "test-req-1",
			Method: "POST",
			Path:   "/test-path",
			Headers: map[string][]string{
				"Content-Type":      {"application/json"},
				"X-Forwarded-Proto": {"https"},
				"X-Forwarded-SSL":   {"on"},
			},
			Body: []byte(`{"test": "data"}`),
		}
		conn.WriteJSON(req)

		// Read response
		var resp TunnelResponse
		if err := conn.ReadJSON(&resp); err != nil {
			t.Errorf("Failed to read response: %v", err)
		}

		// Verify response
		if resp.ID != "test-req-1" {
			t.Errorf("Expected response ID test-req-1, got %s", resp.ID)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status code 200, got %d", resp.StatusCode)
		}
	}))
	defer wsServer.Close()

	// Create client and expose port
	wsURL, _ := url.Parse(wsServer.URL)
	wsURL.Scheme = "ws"
	client := NewClient(wsURL.String())

	// Run ExposePort in a goroutine
	done := make(chan error)
	go func() {
		var portInt int
		if port != "" {
			fmt.Sscanf(port, "%d", &portInt)
		} else {
			portInt = 80
		}
		done <- client.ExposePort(portInt)
	}()

	// Wait a bit for the connection to establish
	time.Sleep(100 * time.Millisecond)

	// The test will complete when the WebSocket server closes
	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Test timeout")
	}
}

func TestSendErrorResponse(t *testing.T) {
	// Create WebSocket test server
	received := make(chan TunnelResponse)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read response
		var resp TunnelResponse
		if err := conn.ReadJSON(&resp); err == nil {
			received <- resp
		}
	}))
	defer server.Close()

	// Create client
	u, _ := url.Parse(server.URL)
	u.Scheme = "ws"
	client := NewClient(u.String())

	// Connect
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	client.conn = conn
	defer client.Close()

	// Send error response
	client.sendErrorResponse("test-req", http.StatusBadGateway, "Test error")

	// Verify response
	select {
	case resp := <-received:
		if resp.ID != "test-req" {
			t.Errorf("Expected ID test-req, got %s", resp.ID)
		}
		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("Expected status code 502, got %d", resp.StatusCode)
		}
		if string(resp.Body) != "Test error" {
			t.Errorf("Expected body 'Test error', got %s", string(resp.Body))
		}
		if resp.Headers["Content-Type"][0] != "text/plain" {
			t.Errorf("Expected Content-Type text/plain, got %s", resp.Headers["Content-Type"][0])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}
}