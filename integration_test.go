package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kidandcat/online/client"
	"github.com/kidandcat/online/server"
)

func TestEndToEndTunneling(t *testing.T) {
	// Create a local test server that will receive forwarded requests
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back request information
		response := map[string]interface{}{
			"method":  r.Method,
			"path":    r.URL.Path,
			"headers": r.Header,
		}
		
		// Read body
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			response["body"] = string(body)
		}
		
		// Strip tunnel ID from path for testing
		path := r.URL.Path
		parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
		if len(parts) > 1 {
			// Remove the first part (tunnel ID) from the path
			path = "/" + strings.Join(parts[1:], "/")
		} else if len(parts) == 1 && parts[0] != "" {
			// Path is just the tunnel ID
			path = "/"
		}
		response["path"] = path
		
		// Check specific endpoints
		switch path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
		case "/echo":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		case "/static/style.css":
			w.Header().Set("Content-Type", "text/css")
			w.Write([]byte("body { color: red; }"))
		default:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer localServer.Close()
	
	// Extract port from local server
	parts := strings.Split(localServer.URL, ":")
	localPort := parts[len(parts)-1]
	
	// Create tunnel manager
	tm := server.NewTunnelManager()
	
	// Create tunnel server
	tunnelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws/tunnel" {
			// Upgrade to WebSocket
			conn, err := server.Upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Fatalf("Failed to upgrade connection: %v", err)
			}
			
			// Create tunnel
			tunnel, err := tm.CreateTunnel(conn)
			if err != nil {
				conn.Close()
				return
			}
			
			// Send tunnel info
			info := map[string]string{
				"id":  tunnel.ID,
				"url": fmt.Sprintf("%s/%s", r.Host, tunnel.ID),
			}
			if err := conn.WriteJSON(info); err != nil {
				t.Fatalf("Failed to send tunnel info: %v", err)
			}
			
			// Keep connection open
			select {}
		} else {
			// Handle tunnel requests
			pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(pathParts) >= 1 {
				// Get active tunnel
				tunnel, exists := tm.GetActiveTunnel()
				if !exists {
					http.Error(w, "No active tunnel", http.StatusNotFound)
					return
				}
				
				// Forward request
				tunnel.ForwardRequest(w, r)
			} else {
				http.Error(w, "Not found", http.StatusNotFound)
			}
		}
	}))
	defer tunnelServer.Close()
	
	// Create client
	c := client.NewClient(tunnelServer.URL)
	
	// Start client in goroutine
	clientDone := make(chan error)
	go func() {
		var port int
		fmt.Sscanf(localPort, "%d", &port)
		clientDone <- c.ExposePort(port)
	}()
	
	// Give client time to connect
	time.Sleep(200 * time.Millisecond)
	
	// Get tunnel URL
	tunnel, exists := tm.GetActiveTunnel()
	if !exists {
		t.Fatal("No active tunnel found")
	}
	
	tunnelURL := fmt.Sprintf("%s/%s", tunnelServer.URL, tunnel.ID)
	
	// Test 1: Simple GET request
	t.Run("SimpleGET", func(t *testing.T) {
		resp, err := http.Get(tunnelURL + "/health")
		if err != nil {
			t.Fatalf("Failed to make request: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
		
		var result map[string]string
		json.NewDecoder(resp.Body).Decode(&result)
		if result["status"] != "healthy" {
			t.Errorf("Expected status healthy, got %s", result["status"])
		}
	})
	
	// Test 2: POST request with body
	t.Run("POSTWithBody", func(t *testing.T) {
		payload := map[string]string{"message": "hello"}
		body, _ := json.Marshal(payload)
		
		resp, err := http.Post(tunnelURL+"/echo", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("Failed to make request: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
		
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		
		if result["method"] != "POST" {
			t.Errorf("Expected method POST, got %s", result["method"])
		}
		if result["path"] != "/echo" {
			t.Errorf("Expected path /echo, got %s", result["path"])
		}
		if result["body"] != `{"message":"hello"}` {
			t.Errorf("Expected body {\"message\":\"hello\"}, got %s", result["body"])
		}
	})
	
	// Test 3: Static file with content type correction
	t.Run("StaticFileContentType", func(t *testing.T) {
		resp, err := http.Get(tunnelURL + "/static/style.css")
		if err != nil {
			t.Fatalf("Failed to make request: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
		
		contentType := resp.Header.Get("Content-Type")
		if contentType != "text/css" {
			t.Errorf("Expected Content-Type text/css, got %s", contentType)
		}
		
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "body { color: red; }" {
			t.Errorf("Expected CSS content, got %s", string(body))
		}
	})
	
	// Test 4: Headers forwarding
	t.Run("HeadersForwarding", func(t *testing.T) {
		req, _ := http.NewRequest("GET", tunnelURL+"/echo", nil)
		req.Header.Set("X-Custom-Header", "test-value")
		req.Header.Set("Authorization", "Bearer token123")
		
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request: %v", err)
		}
		defer resp.Body.Close()
		
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		
		headers, ok := result["headers"].(map[string]interface{})
		if !ok {
			t.Fatal("Headers not found in response")
		}
		
		// Check custom header was forwarded
		if customHeader, ok := headers["X-Custom-Header"].([]interface{}); ok {
			if len(customHeader) == 0 || customHeader[0] != "test-value" {
				t.Errorf("Expected X-Custom-Header to be test-value, got %v", customHeader)
			}
		} else {
			t.Error("X-Custom-Header not found")
		}
		
		// Check authorization header was forwarded
		if authHeader, ok := headers["Authorization"].([]interface{}); ok {
			if len(authHeader) == 0 || authHeader[0] != "Bearer token123" {
				t.Errorf("Expected Authorization header to be Bearer token123, got %v", authHeader)
			}
		} else {
			t.Error("Authorization header not found")
		}
	})
	
	// Test 5: Concurrent requests
	t.Run("ConcurrentRequests", func(t *testing.T) {
		done := make(chan bool, 5)
		errors := make(chan error, 5)
		
		// Make 5 concurrent requests
		for i := 0; i < 5; i++ {
			go func(id int) {
				resp, err := http.Get(fmt.Sprintf("%s/concurrent-%d", tunnelURL, id))
				if err != nil {
					errors <- err
					done <- true
					return
				}
				defer resp.Body.Close()
				
				if resp.StatusCode != http.StatusOK {
					errors <- fmt.Errorf("request %d: expected status 200, got %d", id, resp.StatusCode)
				}
				
				var result map[string]interface{}
				json.NewDecoder(resp.Body).Decode(&result)
				
				expectedPath := fmt.Sprintf("/concurrent-%d", id)
				if result["path"] != expectedPath {
					errors <- fmt.Errorf("request %d: expected path %s, got %s", id, expectedPath, result["path"])
				}
				
				done <- true
			}(i)
		}
		
		// Wait for all requests
		for i := 0; i < 5; i++ {
			<-done
		}
		
		// Check for errors
		close(errors)
		for err := range errors {
			t.Error(err)
		}
	})
	
	// Cleanup
	c.Close()
	tm.RemoveTunnel()
}

func TestMultipleTunnelAttempts(t *testing.T) {
	// Create tunnel manager
	tm := server.NewTunnelManager()
	
	// Create server
	tunnelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws/tunnel" {
			conn, err := server.Upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			
			tunnel, err := tm.CreateTunnel(conn)
			if err != nil {
				// Send error response
				conn.WriteJSON(map[string]string{"error": err.Error()})
				conn.Close()
				return
			}
			
			// Send success response
			conn.WriteJSON(map[string]string{
				"id":  tunnel.ID,
				"url": fmt.Sprintf("%s/%s", r.Host, tunnel.ID),
			})
			
			// Keep connection open
			select {}
		}
	}))
	defer tunnelServer.Close()
	
	// First client connects successfully
	wsURL := "ws" + strings.TrimPrefix(tunnelServer.URL, "http") + "/ws/tunnel"
	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("First client failed to connect: %v", err)
	}
	defer conn1.Close()
	
	var info1 map[string]string
	if err := conn1.ReadJSON(&info1); err != nil {
		t.Fatalf("Failed to read first tunnel info: %v", err)
	}
	
	if info1["error"] != "" {
		t.Fatalf("First client got unexpected error: %s", info1["error"])
	}
	
	// Second client should fail
	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Second client failed to connect: %v", err)
	}
	defer conn2.Close()
	
	var info2 map[string]string
	if err := conn2.ReadJSON(&info2); err != nil {
		t.Fatalf("Failed to read second tunnel info: %v", err)
	}
	
	if info2["error"] == "" {
		t.Fatal("Expected second client to get an error")
	}
	if !strings.Contains(info2["error"], "already active") {
		t.Errorf("Expected 'already active' error, got: %s", info2["error"])
	}
}

func TestRequestTimeout(t *testing.T) {
	// Create a slow local server
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("slow response"))
	}))
	defer localServer.Close()
	
	// Extract port
	parts := strings.Split(localServer.URL, ":")
	localPort := parts[len(parts)-1]
	
	// Create tunnel infrastructure
	tm := server.NewTunnelManager()
	tunnelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws/tunnel" {
			conn, _ := server.Upgrader.Upgrade(w, r, nil)
			tunnel, _ := tm.CreateTunnel(conn)
			conn.WriteJSON(map[string]string{
				"id":  tunnel.ID,
				"url": fmt.Sprintf("%s/%s", r.Host, tunnel.ID),
			})
			select {}
		} else {
			tunnel, exists := tm.GetActiveTunnel()
			if exists {
				tunnel.ForwardRequest(w, r)
			}
		}
	}))
	defer tunnelServer.Close()
	
	// Start client
	c := client.NewClient(tunnelServer.URL)
	go func() {
		var port int
		fmt.Sscanf(localPort, "%d", &port)
		c.ExposePort(port)
	}()
	
	time.Sleep(200 * time.Millisecond)
	
	// Get tunnel
	tunnel, _ := tm.GetActiveTunnel()
	tunnelURL := fmt.Sprintf("%s/%s/timeout", tunnelServer.URL, tunnel.ID)
	
	// Make request with short timeout
	client := &http.Client{
		Timeout: 500 * time.Millisecond,
	}
	
	start := time.Now()
	_, err := client.Get(tunnelURL)
	duration := time.Since(start)
	
	if err == nil {
		t.Error("Expected timeout error")
	}
	
	// Should timeout within reasonable time
	if duration > 1*time.Second {
		t.Errorf("Request took too long: %v", duration)
	}
}